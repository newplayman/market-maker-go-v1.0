package runner

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	gateway "github.com/newplayman/market-maker-phoenix/internal/exchange"
	"github.com/newplayman/market-maker-phoenix/internal/metrics"
	"github.com/newplayman/market-maker-phoenix/internal/order"
	"github.com/newplayman/market-maker-phoenix/internal/risk"
	"github.com/newplayman/market-maker-phoenix/internal/store"
	"github.com/newplayman/market-maker-phoenix/internal/strategy"
	"github.com/rs/zerolog/log"
)

// Runner 核心运行器
type Runner struct {
	cfg      *config.Config
	store    *store.Store
	strategy strategy.Strategy
	risk     *risk.RiskManager
	exchange gateway.Exchange
	om       *order.OrderManager
	dryRun   bool

	wg       sync.WaitGroup
	stopChan chan struct{}
	stopped  bool
	mu       sync.Mutex
}

// NewRunner 创建Runner实例
func NewRunner(
	cfg *config.Config,
	st *store.Store,
	strat strategy.Strategy,
	riskMgr *risk.RiskManager,
	exch gateway.Exchange,
) *Runner {
	om := order.NewOrderManager(st, exch)
	return &Runner{
		cfg:      cfg,
		store:    st,
		strategy: strat,
		risk:     riskMgr,
		exchange: exch,
		om:       om,
		stopChan: make(chan struct{}),
	}
}

// Start 启动Runner
func (r *Runner) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return fmt.Errorf("runner已停止，无法重新启动")
	}
	r.mu.Unlock()

	// 连接交易所
	log.Info().Msg("正在连接交易所...")
	if err := r.exchange.Connect(ctx); err != nil {
		return fmt.Errorf("连接交易所失败: %w", err)
	}
	log.Info().Msg("交易所连接成功")

	// 启动深度流
	symbols := make([]string, 0, len(r.cfg.Symbols))
	for _, symCfg := range r.cfg.Symbols {
		symbols = append(symbols, symCfg.Symbol)
	}
	log.Info().Strs("symbols", symbols).Msg("正在启动深度流...")
	if err := r.exchange.StartDepthStream(ctx, symbols, r.onDepthUpdate); err != nil {
		return fmt.Errorf("启动深度流失败: %w", err)
	}
	log.Info().Msg("深度流启动成功")

	// 启动用户数据流
	log.Info().Msg("正在启动用户数据流...")
	callbacks := &gateway.UserStreamCallbacks{
		OnOrderUpdate:   r.onOrderUpdate,
		OnAccountUpdate: r.onAccountUpdate,
		OnFunding:       r.onFundingUpdate,
	}
	if err := r.exchange.StartUserStream(ctx, callbacks); err != nil {
		return fmt.Errorf("启动用户数据流失败: %w", err)
	}
	log.Info().Msg("用户数据流启动成功")

	// 为每个交易对启动独立的协程
	for _, symCfg := range r.cfg.Symbols {
		r.wg.Add(1)
		go r.runSymbol(ctx, symCfg.Symbol)
	}

	// 启动全局监控协程
	r.wg.Add(1)
	go r.runGlobalMonitor(ctx)

	log.Info().Msg("Runner启动完成")
	return nil
}

// Stop 停止Runner
func (r *Runner) Stop() {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.stopped = true
	r.mu.Unlock()

	close(r.stopChan)
	r.wg.Wait()

	log.Info().Msg("Runner已停止")
}

// runSymbol 运行单个交易对的做市循环
func (r *Runner) runSymbol(ctx context.Context, symbol string) {
	defer r.wg.Done()

	log.Info().Str("symbol", symbol).Msg("启动交易对做市循环")

	ticker := time.NewTicker(r.cfg.GetQuoteInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info().Str("symbol", symbol).Msg("收到退出信号")
			return
		case <-r.stopChan:
			log.Info().Str("symbol", symbol).Msg("收到停止信号")
			return
		case <-ticker.C:
			if err := r.processSymbol(ctx, symbol); err != nil {
				log.Error().
					Err(err).
					Str("symbol", symbol).
					Msg("处理交易对失败")
				metrics.RecordError("process_symbol", symbol)
			}
		}
	}
}

// processSymbol 处理单个交易对
func (r *Runner) processSymbol(ctx context.Context, symbol string) error {
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime).Seconds()
		metrics.QuoteGeneration.WithLabelValues(symbol).Observe(duration)
	}()

	// 订单溢出熔断阈值
	const orderOverflowThreshold = 50

	// 读取当前活跃订单数量
	activeOrdersCount := r.store.GetActiveOrderCount(symbol)

	if activeOrdersCount > orderOverflowThreshold {
		log.Error().
			Str("symbol", symbol).
			Int("active_orders", activeOrdersCount).
			Msg("订单数量溢出，进行紧急撤单和熔断")

		// 调用交易所撤销所有订单
		if err := r.exchange.CancelAllOrders(ctx, symbol); err != nil {
			log.Error().Err(err).Str("symbol", symbol).Msg("紧急撤单失败")
		}

		// 停止该策略的做市循环 (返回错误停止本轮交易)
		return fmt.Errorf("订单数量溢出(%d)，触发紧急撤单", activeOrdersCount)
	}

	// 1. 检查止损
	if shouldStop, reason := r.risk.CheckStopLoss(symbol); shouldStop {
		log.Warn().
			Str("symbol", symbol).
			Str("reason", reason).
			Msg("触发止损，暂停做市")
		// TODO: 取消所有订单，平仓
		return fmt.Errorf("止损触发: %s", reason)
	}

	// 2. 检查是否需要减仓
	if should, targetSize := r.risk.ShouldReducePosition(symbol); should {
		log.Warn().
			Str("symbol", symbol).
			Float64("target_size", targetSize).
			Msg("仓位过大，建议减仓")
		// TODO: 执行减仓逻辑
	}

	// 3. 生成报价
	buyQuotes, sellQuotes, err := r.strategy.GenerateQuotes(ctx, symbol)
	if err != nil {
		return fmt.Errorf("生成报价失败: %w", err)
	}

	// 4. 验证报价
	if len(buyQuotes) > 0 && len(sellQuotes) > 0 {
		if err := r.risk.ValidateQuotes(symbol, buyQuotes[0].Price, sellQuotes[0].Price); err != nil {
			return fmt.Errorf("报价验证失败: %w", err)
		}
	}

	// 5. 转换为exchange.Order并进行Pre-Trade风控校验
	desiredBuyOrders := make([]*gateway.Order, 0, len(buyQuotes))
	for _, quote := range buyQuotes {
		// Pre-Trade风控检查：每个买单都需要通过风控校验
		if err := r.risk.CheckPreTrade(symbol, "BUY", quote.Size); err != nil {
			log.Warn().
				Err(err).
				Str("symbol", symbol).
				Float64("size", quote.Size).
				Float64("price", quote.Price).
				Msg("买单未通过风控校验，跳过此单")
			continue
		}
		desiredBuyOrders = append(desiredBuyOrders, &gateway.Order{
			Symbol:   symbol,
			Side:     "BUY",
			Type:     "LIMIT",
			Quantity: quote.Size,
			Price:    quote.Price,
		})
	}

	desiredSellOrders := make([]*gateway.Order, 0, len(sellQuotes))
	for _, quote := range sellQuotes {
		// Pre-Trade风控检查：每个卖单都需要通过风控校验
		if err := r.risk.CheckPreTrade(symbol, "SELL", quote.Size); err != nil {
			log.Warn().
				Err(err).
				Str("symbol", symbol).
				Float64("size", quote.Size).
				Float64("price", quote.Price).
				Msg("卖单未通过风控校验，跳过此单")
			continue
		}
		desiredSellOrders = append(desiredSellOrders, &gateway.Order{
			Symbol:   symbol,
			Side:     "SELL",
			Type:     "LIMIT",
			Quantity: quote.Size,
			Price:    quote.Price,
		})
	}

	// 6. 同步当前本地订单状态（从exchange拉取）
	if err := r.om.SyncActiveOrders(ctx, symbol); err != nil {
		log.Error().Err(err).Str("symbol", symbol).Msg("同步活跃订单失败")
		return err
	}

	// 7. 计算订单差分，获取待撤销和待新增订单
	toCancel, toPlace := r.om.CalculateOrderDiff(symbol, desiredBuyOrders, desiredSellOrders)

	// 8. 应用差分，执行撤单和新单下单
	if r.dryRun {
		log.Info().
			Str("symbol", symbol).
			Int("to_cancel", len(toCancel)).
			Int("to_place", len(toPlace)).
			Msg("[Dry-Run模式] 模拟执行订单差分操作，未实际下单")
	} else {
		if err := r.om.ApplyDiff(ctx, symbol, toCancel, toPlace); err != nil {
			log.Error().Err(err).Str("symbol", symbol).Msg("应用订单差分失败")
			return err
		}
	}

	log.Debug().
		Str("symbol", symbol).
		Int("to_cancel", len(toCancel)).
		Int("to_place", len(toPlace)).
		Msg("订单差分处理完成")

	log.Debug().
		Str("symbol", symbol).
		Int("buy_quotes", len(buyQuotes)).
		Int("sell_quotes", len(sellQuotes)).
		Msg("报价已下达")

	// 8. 更新指标
	r.updateSymbolMetrics(symbol)

	return nil
}

// onDepthUpdate 处理深度更新
func (r *Runner) onDepthUpdate(depth *gateway.Depth) {
	if depth == nil || len(depth.Bids) == 0 || len(depth.Asks) == 0 {
		return
	}

	// 更新Store中的市场数据
	bestBid := depth.Bids[0].Price
	bestAsk := depth.Asks[0].Price
	midPrice := (bestBid + bestAsk) / 2.0

	r.store.UpdateMidPrice(depth.Symbol, midPrice, bestBid, bestAsk)

	log.Debug().
		Str("symbol", depth.Symbol).
		Float64("mid", midPrice).
		Float64("bid", bestBid).
		Float64("ask", bestAsk).
		Msg("深度更新")
}

// onOrderUpdate 处理订单更新
func (r *Runner) onOrderUpdate(order *gateway.Order) {
	if order == nil {
		return
	}

	log.Info().
		Str("symbol", order.Symbol).
		Str("side", order.Side).
		Str("status", order.Status).
		Float64("filled", order.FilledQty).
		Msg("订单更新")

	// 如果订单完全成交，记录成交
	if order.Status == "FILLED" {
		// 计算PNL（简化版，实际需要根据仓位计算）
		pnl := 0.0
		r.store.RecordFill(order.Symbol, order.FilledQty, pnl)
		metrics.RecordFill(order.Symbol, order.Side, order.FilledQty)
	}
}

// onAccountUpdate 处理账户更新
func (r *Runner) onAccountUpdate(positions []*gateway.Position) {
	for _, pos := range positions {
		if pos == nil {
			continue
		}

		// 更新Store中的仓位
		storePos := store.Position{
			Symbol:        pos.Symbol,
			Size:          pos.Size,
			EntryPrice:    pos.EntryPrice,
			UnrealizedPNL: pos.UnrealizedPNL,
			Notional:      pos.Notional,
			Leverage:      pos.Leverage,
		}
		r.store.UpdatePosition(pos.Symbol, storePos)

		log.Info().
			Str("symbol", pos.Symbol).
			Float64("size", pos.Size).
			Float64("entry", pos.EntryPrice).
			Float64("pnl", pos.UnrealizedPNL).
			Msg("仓位更新")
	}
}

// onFundingUpdate 处理资金费率更新
func (r *Runner) onFundingUpdate(funding *gateway.FundingRate) {
	if funding == nil {
		return
	}

	// 更新Store中的资金费率
	r.store.UpdateFundingRate(funding.Symbol, funding.Rate)

	log.Info().
		Str("symbol", funding.Symbol).
		Float64("rate", funding.Rate).
		Msg("资金费率更新")
}

// runGlobalMonitor 运行全局监控
func (r *Runner) runGlobalMonitor(ctx context.Context) {
	defer r.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopChan:
			return
		case <-ticker.C:
			r.monitorGlobalState()
		}
	}
}

// monitorGlobalState 监控全局状态
func (r *Runner) monitorGlobalState() {
	totalNotional := r.store.GetTotalNotional()
	metrics.TotalNotional.Set(totalNotional)

	// 检查总名义价值上限
	if totalNotional > r.cfg.Global.TotalNotionalMax {
		log.Warn().
			Float64("total_notional", totalNotional).
			Float64("max", r.cfg.Global.TotalNotionalMax).
			Msg("总名义价值超过上限")
	}

	// 记录所有交易对的风控指标
	for _, symbol := range r.store.GetAllSymbols() {
		r.risk.LogRiskMetrics(symbol)
	}
}

// updateSymbolMetrics 更新交易对指标
func (r *Runner) updateSymbolMetrics(symbol string) {
	state := r.store.GetSymbolState(symbol)
	if state == nil {
		return
	}

	state.Mu.RLock()
	defer state.Mu.RUnlock()

	// 更新仓位指标
	metrics.UpdatePositionMetrics(
		symbol,
		state.Position.Size,
		state.Position.Notional,
		state.Position.UnrealizedPNL,
	)

	// 更新挂单指标
	metrics.UpdatePendingMetrics(
		symbol,
		state.PendingBuy,
		state.PendingSell,
	)

	// 更新市场数据指标
	spread := 0.0
	if state.MidPrice > 0 {
		spread = (state.BestAsk - state.BestBid) / state.MidPrice
	}
	metrics.UpdateMarketMetrics(
		symbol,
		state.MidPrice,
		spread,
		state.FundingRate,
	)

	// 更新风控指标
	metrics.WorstCaseLong.WithLabelValues(symbol).Set(
		r.store.GetWorstCaseLong(symbol),
	)
	metrics.MaxDrawdown.WithLabelValues(symbol).Set(state.MaxDrawdown)
	metrics.CancelRate.WithLabelValues(symbol).Set(float64(state.CancelCountLast))
	metrics.TotalPNL.WithLabelValues(symbol).Set(state.TotalPNL)
}
