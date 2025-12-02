package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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

	// WebSocket reconnection tracking
	lastReconnectTime time.Time
	reconnectMu       sync.Mutex
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

	// 【关键修复】添加Panic恢复机制，防止单个goroutine崩溃导致假死
	defer func() {
		if err := recover(); err != nil {
			log.Error().
				Interface("panic", err).
				Str("symbol", symbol).
				Str("stack", fmt.Sprintf("%v", err)).
				Msg("【严重】runSymbol发生panic！尝试恢复...")

			// 记录panic到metrics
			metrics.RecordError("goroutine_panic", symbol)

			// 等待5秒后尝试重启该goroutine
			time.Sleep(5 * time.Second)

			// 检查是否已经停止
			r.mu.Lock()
			stopped := r.stopped
			r.mu.Unlock()

			if !stopped {
				log.Warn().
					Str("symbol", symbol).
					Msg("重新启动runSymbol goroutine")
				r.wg.Add(1)
				go r.runSymbol(ctx, symbol)
			}
		}
	}()

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

	// 【修复假死】无条件检查并重置撤单计数器（防止假死）
	// 必须在函数开头执行，确保每次循环都会检查
	symCfg := r.cfg.GetSymbolConfig(symbol)
	if symCfg != nil {
		state := r.store.GetSymbolState(symbol)
		if state != nil {
			state.Mu.Lock()
			if time.Since(state.LastCancelReset) > time.Minute {
				oldCount := state.CancelCountLast
				state.CancelCountLast = 0
				state.LastCancelReset = time.Now()
				if oldCount > 0 {
					log.Info().
						Str("symbol", symbol).
						Int("reset_from", oldCount).
						Msg("撤单计数器已重置（每分钟自动）")
				}
			}
			state.Mu.Unlock()
		}
	}

	// 【新增】同步当前本地订单状态（从exchange拉取）
	// 必须在检查溢出前同步，否则一旦溢出就会因状态无法更新而陷入死循环
	if err := r.om.SyncActiveOrders(ctx, symbol); err != nil {
		log.Error().Err(err).Str("symbol", symbol).Msg("同步活跃订单失败")
		return err
	}

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

	// 【关键修复】检查价格数据新鲜度 - 防止WebSocket静默断流导致假死
	// 将阈值从10秒降低到3秒，更快检测异常
	state := r.store.GetSymbolState(symbol)
	if state != nil {
		state.Mu.RLock()
		lastUpdate := state.LastPriceUpdate
		midPrice := state.MidPrice
		state.Mu.RUnlock()

		// 如果价格从未更新（刚启动且WSS未推）或超过3秒未更新
		if lastUpdate.IsZero() || time.Since(lastUpdate) > 3*time.Second {
			log.Error().
				Str("symbol", symbol).
				Time("last_update", lastUpdate).
				Dur("stale_duration", time.Since(lastUpdate)).
				Float64("mid", midPrice).
				Msg("【告警】价格数据过期，停止报价！WebSocket可能断流")
			
			// 记录错误到metrics
			metrics.RecordError("stale_price_data", symbol)
			return nil
		}
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

	// 3. 检查撤单频率，接近限制时记录警告但继续执行
	if symCfg != nil {
		state := r.store.GetSymbolState(symbol)
		if state != nil {
			state.Mu.RLock()
			cancelCount := state.CancelCountLast
			state.Mu.RUnlock()

			// 当撤单数接近限制的95%时，暂停更新以保护账户
			if cancelCount >= int(float64(symCfg.MaxCancelPerMin)*0.95) {
				log.Warn().
					Str("symbol", symbol).
					Int("cancel_count", cancelCount).
					Int("limit", symCfg.MaxCancelPerMin).
					Msg("撤单频率过高，暂停本轮报价更新（保持现有挂单）")
				return nil
			}
		}
	}

	// 4. 生成报价
	// 在生成报价前，重置Pending状态，因为接下来的报价将完全替换现有挂单
	// 这样可以避免RiskManager在Pre-Trade检查时重复计算现有挂单的敞口
	r.store.UpdatePendingOrders(symbol, 0, 0)

	buyQuotes, sellQuotes, err := r.strategy.GenerateQuotes(ctx, symbol)
	if err != nil {
		return fmt.Errorf("生成报价失败: %w", err)
	}

	// 【新增】生成报价后，记录详细网格信息
	if len(buyQuotes) > 0 && len(sellQuotes) > 0 {
		state := r.store.GetSymbolState(symbol)
		mid := 0.0
		currentPos := 0.0
		if state != nil {
			state.Mu.RLock()
			mid = state.MidPrice
			currentPos = state.Position.Size
			state.Mu.RUnlock()
		}

		// 计算买1卖1距离mid
		buy1Distance := mid - buyQuotes[0].Price
		sell1Distance := sellQuotes[0].Price - mid

		// 计算第1-2层间距
		buy12Spacing := 0.0
		sell12Spacing := 0.0
		if len(buyQuotes) >= 2 {
			buy12Spacing = buyQuotes[0].Price - buyQuotes[1].Price
		}
		if len(sellQuotes) >= 2 {
			sell12Spacing = sellQuotes[1].Price - sellQuotes[0].Price
		}

		// 计算最后一层间距（倒数第2到倒数第1层）
		buyLastSpacing := 0.0
		sellLastSpacing := 0.0
		if len(buyQuotes) >= 2 {
			lastIdx := len(buyQuotes) - 1
			buyLastSpacing = buyQuotes[lastIdx-1].Price - buyQuotes[lastIdx].Price
		}
		if len(sellQuotes) >= 2 {
			lastIdx := len(sellQuotes) - 1
			sellLastSpacing = sellQuotes[lastIdx].Price - sellQuotes[lastIdx-1].Price
		}

		log.Info().
			Str("symbol", symbol).
			Float64("mid", mid).
			Float64("pos", currentPos).
			Int("buy_layers", len(buyQuotes)).
			Int("sell_layers", len(sellQuotes)).
			Float64("buy1", buyQuotes[0].Price).
			Float64("sell1", sellQuotes[0].Price).
			Float64("buy1_dist", buy1Distance).
			Float64("sell1_dist", sell1Distance).
			Float64("buy12_spacing", buy12Spacing).
			Float64("sell12_spacing", sell12Spacing).
			Float64("buy_last", buyQuotes[len(buyQuotes)-1].Price).
			Float64("sell_last", sellQuotes[len(sellQuotes)-1].Price).
			Float64("buy_last_spacing", buyLastSpacing).
			Float64("sell_last_spacing", sellLastSpacing).
			Float64("total_buy_size", func() float64 {
				total := 0.0
				for _, q := range buyQuotes {
					total += q.Size
				}
				return total
			}()).
			Float64("total_sell_size", func() float64 {
				total := 0.0
				for _, q := range sellQuotes {
					total += q.Size
				}
				return total
			}()).
			Msg("报价已生成（统一几何网格）")
	}

	// 5. 批量风控检查（新增）- 确保轻仓做市原则
	// 检查所有挂单累计风险，防止满仓
	buyRiskQuotes := make([]risk.Quote, len(buyQuotes))
	for i, q := range buyQuotes {
		buyRiskQuotes[i] = risk.Quote{Price: q.Price, Size: q.Size, Layer: q.Layer}
	}
	sellRiskQuotes := make([]risk.Quote, len(sellQuotes))
	for i, q := range sellQuotes {
		sellRiskQuotes[i] = risk.Quote{Price: q.Price, Size: q.Size, Layer: q.Layer}
	}

	if err := r.risk.CheckBatchPreTrade(symbol, buyRiskQuotes, sellRiskQuotes); err != nil {
		log.Warn().
			Err(err).
			Str("symbol", symbol).
			Int("buy_quotes", len(buyQuotes)).
			Int("sell_quotes", len(sellQuotes)).
			Msg("批量风控检查失败，调整报价数量")

		// 根据风控结果调整报价数量/大小
		buyQuotes, sellQuotes = r.adjustQuotesForRisk(symbol, buyQuotes, sellQuotes)

		log.Info().
			Str("symbol", symbol).
			Int("adjusted_buy_quotes", len(buyQuotes)).
			Int("adjusted_sell_quotes", len(sellQuotes)).
			Msg("报价已根据风控要求调整")
	}

	// 6. 验证报价
	if len(buyQuotes) > 0 && len(sellQuotes) > 0 {
		if err := r.risk.ValidateQuotes(symbol, buyQuotes[0].Price, sellQuotes[0].Price); err != nil {
			return fmt.Errorf("报价验证失败: %w", err)
		}
	}

	// 7. 转换为exchange.Order并进行Pre-Trade风控校验
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

	// 8. 同步当前本地订单状态（已移至函数开头）
	// if err := r.om.SyncActiveOrders(ctx, symbol); err != nil { ... }

	// 9. 计算订单差分，获取待撤销和待新增订单
	symCfg = r.cfg.GetSymbolConfig(symbol)

	// 计算防闪烁容差 (Anti-Flicker Tolerance)
	// 【关键】容差决定了何时撤单重挂，容差越大，撤单频率越低
	// 策略：使用第一层间距作为基准，容差设为其50-80%
	state = r.store.GetSymbolState(symbol)
	tolerance := symCfg.TickSize * 5 // 默认最小容差: 5个tick (0.05 USDT)

	if state != nil && state.MidPrice > 0 {
		// 【新】优先使用统一几何网格参数
		var layerSpacing float64
		if symCfg.GridFirstSpacing > 0 {
			// 使用新配置：第一层间距（USDT绝对值）
			layerSpacing = symCfg.GridFirstSpacing
		} else if symCfg.NearLayerStartOffset > 0 {
			// 兼容旧配置：近端起始偏移（比例）
			layerSpacing = state.MidPrice * symCfg.NearLayerStartOffset
		} else {
			// 默认值：约0.5%的价格波动
			layerSpacing = state.MidPrice * 0.005
		}

		// 【防闪烁】容差 = 层间距 × 90%
		// 这意味着只有当价格偏离超过90%的层间距时才撤单重挂
		// 例如：层间距1.2U，容差1.08U，价格波动<1.08U不会触发撤单
		// 这是非常保守的策略，优先保持订单稳定性而非追求完美价格
		tolerance = layerSpacing * 0.9

		// 设置容差范围限制
		minTolerance := symCfg.TickSize * 10                    // 最小10个tick (0.1 USDT)
		maxTolerance := state.MidPrice * symCfg.MinSpread * 3.0 // 最大为MinSpread的3倍

		if tolerance < minTolerance {
			tolerance = minTolerance
		}
		if tolerance > maxTolerance {
			tolerance = maxTolerance
		}

		log.Info().
			Str("symbol", symbol).
			Float64("mid", state.MidPrice).
			Float64("layer_spacing", layerSpacing).
			Float64("tolerance", tolerance).
			Float64("tolerance_usdt", tolerance).
			Float64("tolerance_pct", tolerance/state.MidPrice*100).
			Msg("防闪烁容差计算完成")
	}

	toCancel, toPlace := r.om.CalculateOrderDiff(symbol, desiredBuyOrders, desiredSellOrders, tolerance)

	// 10. 应用差分，执行撤单和新单下单
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

	// 10. 更新指标
	r.updateSymbolMetrics(symbol)

	return nil
}

// adjustQuotesForRisk 根据风控要求调整报价数量和大小
// 当批量风控检查失败时，削减挂单层数以满足轻仓做市原则
func (r *Runner) adjustQuotesForRisk(symbol string, buyQuotes, sellQuotes []strategy.Quote) ([]strategy.Quote, []strategy.Quote) {
	symCfg := r.cfg.GetSymbolConfig(symbol)
	if symCfg == nil {
		return buyQuotes, sellQuotes
	}

	state := r.store.GetSymbolState(symbol)
	if state == nil {
		return buyQuotes, sellQuotes
	}

	state.Mu.RLock()
	currentPos := state.Position.Size
	state.Mu.RUnlock()

	// 计算当前仓位比例
	posRatio := math.Abs(currentPos) / symCfg.NetMax

	// 轻仓做市原则：最坏情况敞口不应超过NetMax的50%
	maxWorstCase := symCfg.NetMax * 0.5

	// 计算允许的最大挂单总量
	var maxBuySize, maxSellSize float64
	if currentPos > 0 {
		// 多头仓位：限制买单，放松卖单
		maxBuySize = maxWorstCase - math.Abs(currentPos)
		maxSellSize = maxWorstCase + math.Abs(currentPos)
	} else if currentPos < 0 {
		// 空头仓位：限制卖单，放松买单
		maxBuySize = maxWorstCase + math.Abs(currentPos)
		maxSellSize = maxWorstCase - math.Abs(currentPos)
	} else {
		// 无仓位：双边对称
		maxBuySize = maxWorstCase
		maxSellSize = maxWorstCase
	}

	// 确保至少为正数
	if maxBuySize < 0 {
		maxBuySize = 0
	}
	if maxSellSize < 0 {
		maxSellSize = 0
	}

	// 削减买单
	adjustedBuyQuotes := make([]strategy.Quote, 0, len(buyQuotes))
	totalBuySize := 0.0
	for _, q := range buyQuotes {
		if totalBuySize+q.Size <= maxBuySize {
			adjustedBuyQuotes = append(adjustedBuyQuotes, q)
			totalBuySize += q.Size
		} else {
			// 尝试部分添加
			remainingSize := maxBuySize - totalBuySize
			if remainingSize >= symCfg.MinQty {
				adjustedBuyQuotes = append(adjustedBuyQuotes, strategy.Quote{
					Price: q.Price,
					Size:  remainingSize,
					Layer: q.Layer,
				})
			}
			break
		}
	}

	// 削减卖单
	adjustedSellQuotes := make([]strategy.Quote, 0, len(sellQuotes))
	totalSellSize := 0.0
	for _, q := range sellQuotes {
		if totalSellSize+q.Size <= maxSellSize {
			adjustedSellQuotes = append(adjustedSellQuotes, q)
			totalSellSize += q.Size
		} else {
			// 尝试部分添加
			remainingSize := maxSellSize - totalSellSize
			if remainingSize >= symCfg.MinQty {
				adjustedSellQuotes = append(adjustedSellQuotes, strategy.Quote{
					Price: q.Price,
					Size:  remainingSize,
					Layer: q.Layer,
				})
			}
			break
		}
	}

	log.Info().
		Str("symbol", symbol).
		Float64("pos", currentPos).
		Float64("pos_ratio", posRatio).
		Int("original_buy_layers", len(buyQuotes)).
		Int("original_sell_layers", len(sellQuotes)).
		Int("adjusted_buy_layers", len(adjustedBuyQuotes)).
		Int("adjusted_sell_layers", len(adjustedSellQuotes)).
		Float64("total_buy_size", totalBuySize).
		Float64("total_sell_size", totalSellSize).
		Float64("max_buy_allowed", maxBuySize).
		Float64("max_sell_allowed", maxSellSize).
		Msg("根据风控要求调整报价")

	return adjustedBuyQuotes, adjustedSellQuotes
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

		// Log structured TRADE_EVENT for dashboard
		tradeEvent := map[string]interface{}{
			"type":      "TRADE",
			"symbol":    order.Symbol,
			"side":      order.Side,
			"price":     order.Price,
			"quantity":  order.FilledQty,
			"pnl":       pnl,
			"timestamp": time.Now().Unix(),
		}
		jsonBytes, _ := json.Marshal(tradeEvent)
		log.Info().RawJSON("trade_data", jsonBytes).Msg("TRADE_EVENT")
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

	// 【关键修复】添加Panic恢复机制
	defer func() {
		if err := recover(); err != nil {
			log.Error().
				Interface("panic", err).
				Str("stack", fmt.Sprintf("%v", err)).
				Msg("【严重】runGlobalMonitor发生panic！尝试恢复...")

			// 记录panic到metrics
			metrics.RecordError("monitor_panic", "global")

			// 等待5秒后尝试重启
			time.Sleep(5 * time.Second)

			// 检查是否已经停止
			r.mu.Lock()
			stopped := r.stopped
			r.mu.Unlock()

			if !stopped {
				log.Warn().Msg("重新启动runGlobalMonitor goroutine")
				r.wg.Add(1)
				go r.runGlobalMonitor(ctx)
			}
		}
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	log.Info().Msg("Global monitor started")
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.stopChan:
			return
		case <-ticker.C:
			// log.Info().Msg("Global monitor tick")
			r.monitorGlobalState()
			r.logDashboardStats()
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

	// 【关键修复】检查WebSocket健康度 - 检测静默断流
	for _, symbol := range r.store.GetAllSymbols() {
		state := r.store.GetSymbolState(symbol)
		if state != nil {
			state.Mu.RLock()
			lastUpdate := state.LastPriceUpdate
			midPrice := state.MidPrice
			state.Mu.RUnlock()

			// 如果深度数据超过5秒未更新，说明WebSocket可能断流
			if !lastUpdate.IsZero() && time.Since(lastUpdate) > 5*time.Second {
				log.Error().
					Str("symbol", symbol).
					Time("last_update", lastUpdate).
					Dur("stale_duration", time.Since(lastUpdate)).
					Float64("last_mid_price", midPrice).
					Msg("【严重告警】深度数据停止更新，WebSocket可能断流！")

				// 记录错误
				metrics.RecordError("websocket_stale", symbol)

				// 触发WebSocket重连（带防抖）
				r.tryReconnectWebSocket()
			}
		}

		// 记录所有交易对的风控指标
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

// logDashboardStats 记录Dashboard所需的结构化统计信息
// logDashboardStats 记录Dashboard所需的结构化统计信息
func (r *Runner) logDashboardStats() {
	// 获取账户余额
	walletBalance, unrealizedPNL, err := r.exchange.GetAccountBalance(context.Background())
	if err != nil {
		log.Error().Err(err).Msg("获取账户余额失败")
		// 继续执行，使用默认值
	}

	for _, symbol := range r.store.GetAllSymbols() {
		state := r.store.GetSymbolState(symbol)
		if state == nil {
			continue
		}

		state.Mu.RLock()
		stats := map[string]interface{}{
			"type":           "TICKER",
			"symbol":         symbol,
			"mid_price":      state.MidPrice,
			"position":       state.Position.Size,
			"entry_price":    state.Position.EntryPrice,
			"unrealized_pnl": state.Position.UnrealizedPNL,
			"total_pnl":      state.TotalPNL,
			"active_orders":  state.ActiveOrderCount,
			"total_notional": r.store.GetTotalNotional(),
			"fill_count":     state.FillCount,
			"wallet_balance": walletBalance,
			"account_pnl":    unrealizedPNL,
			"net_value":      walletBalance + unrealizedPNL,
		}
		state.Mu.RUnlock()

		jsonBytes, _ := json.Marshal(stats)
		log.Info().RawJSON("ticker_data", jsonBytes).Msg("TICKER_EVENT")
	}
}

// tryReconnectWebSocket 尝试重连WebSocket，带防抖机制
func (r *Runner) tryReconnectWebSocket() {
	r.reconnectMu.Lock()
	defer r.reconnectMu.Unlock()

	// 防抖：距离上次重连至少30秒
	if time.Since(r.lastReconnectTime) < 30*time.Second {
		log.Debug().Msg("重连请求被防抖限制，距离上次重连时间过短")
		return
	}

	r.lastReconnectTime = time.Now()

	// 在新 goroutine 中执行重连，避免阻塞
	go func() {
		log.Warn().Msg("尝试重连 WebSocket 流...")
		ctx := context.Background()
		if err := r.exchange.ReconnectStreams(ctx); err != nil {
			log.Error().Err(err).Msg("WebSocket 重连失败")
		} else {
			log.Info().Msg("WebSocket 重连成功")
		}
	}()
}
