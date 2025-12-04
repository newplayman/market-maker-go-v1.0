package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
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

	// 【P0-3】WebSocket重连状态管理(统一)
	reconnectMu           sync.Mutex
	lastReconnectTime     time.Time
	reconnectAttempts     int  // 重连尝试次数
	reconnectInProgress   bool // 是否正在重连中
	reconnectSuccessCount int  // 重连成功次数
	reconnectFailCount    int  // 重连失败次数

	// 【P1-1】WebSocket消息处理解耦
	depthChan      chan *gateway.Depth // 深度消息缓冲channel
	depthDropCount int64               // 丢弃的消息数(背压时)

	// 看门狗/安全模式
	safeModeMu     sync.RWMutex
	safeMode       bool
	safeModeReason string

	// 极限风控
	emergencyMu         sync.Mutex
	lastEmergencyAction map[string]time.Time
	emergencyOrders     map[string]*emergencyOrder
}

type emergencyOrder struct {
	Symbol    string
	Side      string
	Quantity  float64
	OrderType string
	CreatedAt time.Time
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
		cfg:                 cfg,
		store:               st,
		strategy:            strat,
		risk:                riskMgr,
		exchange:            exch,
		om:                  om,
		stopChan:            make(chan struct{}),
		lastEmergencyAction: make(map[string]time.Time),
		emergencyOrders:     make(map[string]*emergencyOrder),
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

	// 提前初始化深度通道，避免WebSocket开始推送时channel尚未就绪
	if r.depthChan == nil {
		r.depthChan = make(chan *gateway.Depth, DEPTH_CHANNEL_BUFFER_SIZE)
		log.Info().
			Int("channel_cap", DEPTH_CHANNEL_BUFFER_SIZE).
			Int("workers", DEPTH_PROCESSOR_WORKERS).
			Msg("初始化深度消息处理管线")

		for workerID := 1; workerID <= DEPTH_PROCESSOR_WORKERS; workerID++ {
			r.wg.Add(1)
			go r.runDepthProcessor(ctx, workerID)
		}
	}

	// 连接交易所
	log.Info().Msg("正在连接交易所...")
	if err := r.exchange.Connect(ctx); err != nil {
		return fmt.Errorf("连接交易所失败: %w", err)
	}
	log.Info().Msg("交易所连接成功")

	// 【新增】设置全仓/逐仓模式 (逐仓 ISOLATED)
	for _, symCfg := range r.cfg.Symbols {
		log.Info().Str("symbol", symCfg.Symbol).Str("type", "ISOLATED").Msg("设置逐仓模式")
		if err := r.exchange.SetMarginType(ctx, symCfg.Symbol, "ISOLATED"); err != nil {
			// 如果已经是逐仓模式，币安会返回错误，这里仅记录警告
			log.Warn().Err(err).Str("symbol", symCfg.Symbol).Msg("设置逐仓模式失败 (可能是已设置)")
		}
	}

	// 【新增】设置杠杆倍数 (默认20X)
	for _, symCfg := range r.cfg.Symbols {
		log.Info().Str("symbol", symCfg.Symbol).Int("leverage", 20).Msg("设置杠杆倍数")
		if err := r.exchange.SetLeverage(ctx, symCfg.Symbol, 20); err != nil {
			log.Warn().Err(err).Str("symbol", symCfg.Symbol).Msg("设置杠杆失败，将使用默认杠杆")
			// 不阻断启动，继续执行
		}
	}

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

	if ok, reason := r.inSafeMode(); ok {
		log.Debug().
			Str("symbol", symbol).
			Str("reason", reason).
			Msg("安全模式生效，跳过本轮做市")
		return nil
	}

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
	const orderOverflowThreshold = ORDER_OVERFLOW_THRESHOLD

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

		// 【修复断流】将检测阈值从3秒降低到2秒，更快检测断流
		if lastUpdate.IsZero() || time.Since(lastUpdate) > STALE_PRICE_THRESHOLD_SECONDS*time.Second {
			staleDuration := time.Since(lastUpdate)
			log.Error().
				Str("symbol", symbol).
				Time("last_update", lastUpdate).
				Dur("stale_duration", staleDuration).
				Float64("mid", midPrice).
				Msg("【告警】价格数据过期，停止报价！WebSocket可能断流")

			// 记录错误到metrics
			metrics.RecordError("stale_price_data", symbol)

			if drained := r.drainDepthChannel("stale_price"); drained > 0 {
				log.Warn().
					Str("symbol", symbol).
					Int("drained", drained).
					Msg("检测到价格过期，已主动丢弃堆积的深度消息")
			}

			r.refreshMidPriceFromREST(ctx, symbol)

			// 【修复断流】检测到断流时立即触发重连（不等待global monitor）
			r.tryReconnectWebSocket()

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
	metrics.UpdateGridLayerMetrics(symbol, len(buyQuotes), len(sellQuotes))

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

	// 5. 根据持仓分级风控调整网格
	buyQuotes, sellQuotes, guardHalt := r.applyPositionGuards(ctx, symbol, buyQuotes, sellQuotes)
	if guardHalt {
		log.Warn().
			Str("symbol", symbol).
			Msg("极限风控动作进行中，本轮跳过下单循环")
		return nil
	}

	buyQuotes = r.enforceQuotePrecision(symbol, buyQuotes, symCfg, "BUY")
	sellQuotes = r.enforceQuotePrecision(symbol, sellQuotes, symCfg, "SELL")

	// 6. 批量风控检查（新增）- 确保轻仓做市原则
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
		buyQuotes = r.enforceQuotePrecision(symbol, buyQuotes, symCfg, "BUY")
		sellQuotes = r.enforceQuotePrecision(symbol, sellQuotes, symCfg, "SELL")

		log.Info().
			Str("symbol", symbol).
			Int("adjusted_buy_quotes", len(buyQuotes)).
			Int("adjusted_sell_quotes", len(sellQuotes)).
			Msg("报价已根据风控要求调整")
	}

	// 7. 验证报价
	if len(buyQuotes) > 0 && len(sellQuotes) > 0 {
		if err := r.risk.ValidateQuotes(symbol, buyQuotes[0].Price, sellQuotes[0].Price); err != nil {
			return fmt.Errorf("报价验证失败: %w", err)
		}
	}

	// 8. 转换为exchange.Order并进行Pre-Trade风控校验
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

	// 9. 同步当前本地订单状态（已移至函数开头）
	// if err := r.om.SyncActiveOrders(ctx, symbol); err != nil { ... }

	// 10. 计算订单差分，获取待撤销和待新增订单
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

	// 11. 应用差分，执行撤单和新单下单
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

	// 12. 更新指标
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

	// 轻仓做市原则：最坏情况敞口不应超过NetMax的150%
	maxWorstCase := symCfg.NetMax * 1.5 // 【修复】从0.5改为1.5，与风控保持一致

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

// applyPositionGuards 根据持仓/浮亏触发多级风控
func (r *Runner) applyPositionGuards(ctx context.Context, symbol string, buyQuotes, sellQuotes []strategy.Quote) ([]strategy.Quote, []strategy.Quote, bool) {
	symCfg := r.cfg.GetSymbolConfig(symbol)
	if symCfg == nil {
		return buyQuotes, sellQuotes, false
	}

	guardBlock := symCfg.GuardBlockRatio
	if guardBlock <= 0 {
		guardBlock = 0.55
	}
	guardFlatten := symCfg.GuardFlattenRatio
	if guardFlatten <= guardBlock {
		guardFlatten = guardBlock + 0.2
	}
	guardLiquidate := symCfg.GuardLiquidateRatio
	if guardLiquidate <= guardFlatten {
		guardLiquidate = guardFlatten + 0.2
	}
	guardPnLStop := symCfg.GuardPnLStopRatio
	if guardPnLStop <= 0 {
		guardPnLStop = 0.08
	}
	cooldownSec := symCfg.GuardCooldownSec
	if cooldownSec <= 0 {
		cooldownSec = 180
	}
	emergencySlice := symCfg.GuardEmergencySlice
	if emergencySlice <= 0 || emergencySlice > 1 {
		emergencySlice = 1
	}

	state := r.store.GetSymbolState(symbol)
	if state == nil {
		return buyQuotes, sellQuotes, false
	}

	state.Mu.RLock()
	pos := state.Position.Size
	unrealized := state.Position.UnrealizedPNL
	notional := state.Position.Notional
	state.Mu.RUnlock()

	if pos == 0 {
		return buyQuotes, sellQuotes, false
	}

	posRatio := math.Abs(pos) / symCfg.NetMax
	drawdownRatio := 0.0
	if notional > 0 {
		drawdownRatio = math.Abs(unrealized) / notional
	}

	addSide := &buyQuotes
	reduceSide := &sellQuotes
	direction := "LONG"
	if pos < 0 {
		addSide = &sellQuotes
		reduceSide = &buyQuotes
		direction = "SHORT"
	}

	// 阶段1：关闭加仓挂单
	if posRatio >= guardBlock {
		if len(*addSide) > 0 {
			log.Warn().
				Str("symbol", symbol).
				Str("direction", direction).
				Float64("pos_ratio", posRatio).
				Msg("风控阶段1触发：同向挂单全部取消，禁止继续加仓")
		}
		*addSide = nil
	}

	// 阶段2：仅保留磨成本挂单
	if posRatio >= guardFlatten {
		focused := r.focusReduceQuotes(*reduceSide, symCfg.MinQty)
		if len(focused) > 0 {
			log.Warn().
				Str("symbol", symbol).
				Str("direction", direction).
				Float64("pos_ratio", posRatio).
				Int("layers", len(focused)).
				Msg("风控阶段2触发：仅保留磨成本减仓挂单")
		}
		*reduceSide = focused
		*addSide = nil
	}

	// 阶段3：市价极限清仓
	emergencyByPos := posRatio >= guardLiquidate
	emergencyByPNL := unrealized < 0 && drawdownRatio >= guardPnLStop
	if emergencyByPos || emergencyByPNL {
		reason := "pos_ratio"
		if emergencyByPNL {
			reason = "drawdown"
		}
		cooldown := time.Duration(cooldownSec) * time.Second
		if r.isEmergencyCooling(symbol, cooldown) {
			log.Warn().
				Str("symbol", symbol).
				Str("reason", reason).
				Dur("cooldown", cooldown).
				Msg("风控阶段3触发，但处于冷却期，等待上一笔极限单执行")
			return buyQuotes, sellQuotes, true
		}

		qty := math.Abs(pos) * emergencySlice
		if qty < symCfg.MinQty {
			qty = math.Abs(pos)
		}
		side := "SELL"
		if pos < 0 {
			side = "BUY"
		}

		log.Error().
			Str("symbol", symbol).
			Str("reason", reason).
			Float64("pos_ratio", posRatio).
			Float64("drawdown_ratio", drawdownRatio).
			Str("side", side).
			Float64("qty", qty).
			Msg("风控阶段3触发：提交Reduce-Only市价单")

		clientID, err := r.exchange.PlaceReduceOnlyMarket(ctx, symbol, side, qty)
		if err != nil {
			log.Error().
				Err(err).
				Str("symbol", symbol).
				Str("side", side).
				Msg("极限清仓下单失败")

			if r.shouldFallbackReduceOnly(err) {
				if limitPrice, ok := r.getEmergencyLimitPrice(symbol, side, symCfg); ok {
					limitClientID, errLimit := r.exchange.PlaceReduceOnlyLimit(ctx, symbol, side, qty, limitPrice)
					if errLimit != nil {
						log.Error().
							Err(errLimit).
							Str("symbol", symbol).
							Str("side", side).
							Float64("price", limitPrice).
							Msg("极限清仓限价单也失败")
					} else {
						r.trackEmergencyOrder(symbol, limitClientID, side, qty, "limit")
						r.markEmergency(symbol)
						log.Warn().
							Str("symbol", symbol).
							Str("side", side).
							Float64("qty", qty).
							Float64("price", limitPrice).
							Msg("极限清仓通过限价单提交，进入冷却期")
						return buyQuotes, sellQuotes, true
					}
				} else {
					log.Error().
						Str("symbol", symbol).
						Msg("无法获取盘口价格，极限清仓限价单未执行")
				}
			}
		} else {
			r.trackEmergencyOrder(symbol, clientID, side, qty, "market")
			r.markEmergency(symbol)
			log.Warn().
				Str("symbol", symbol).
				Str("side", side).
				Float64("qty", qty).
				Msg("极限清仓下单成功，进入冷却期")
		}
		return buyQuotes, sellQuotes, true
	}

	return buyQuotes, sellQuotes, false
}

func (r *Runner) focusReduceQuotes(quotes []strategy.Quote, minQty float64) []strategy.Quote {
	if len(quotes) == 0 {
		return quotes
	}

	maxLayers := 2
	if len(quotes) < maxLayers {
		maxLayers = len(quotes)
	}

	focused := make([]strategy.Quote, 0, maxLayers)
	for i := 0; i < maxLayers; i++ {
		q := quotes[i]
		boosted := q.Size * 1.5
		if boosted < minQty {
			boosted = math.Max(minQty, q.Size)
		}
		q.Size = boosted
		focused = append(focused, q)
	}
	return focused
}

// enforceQuotePrecision 将报价数量/价格对齐到交易所要求的步长，避免精度错误导致下单失败
func (r *Runner) enforceQuotePrecision(symbol string, quotes []strategy.Quote, symCfg *config.SymbolConfig, side string) []strategy.Quote {
	if len(quotes) == 0 || symCfg == nil {
		return quotes
	}

	minQty := symCfg.MinQty
	tick := symCfg.TickSize

	filtered := make([]strategy.Quote, 0, len(quotes))
	removed := 0

	for _, q := range quotes {
		if minQty > 0 {
			q.Size = roundToStep(q.Size, minQty)
		}
		if q.Size < minQty || q.Size <= 0 {
			removed++
			continue
		}

		if tick > 0 {
			q.Price = roundToStep(q.Price, tick)
		}
		if q.Price <= 0 {
			removed++
			continue
		}

		filtered = append(filtered, q)
	}

	if removed > 0 {
		log.Warn().
			Str("symbol", symbol).
			Str("side", side).
			Int("removed", removed).
			Msg("报价数量/价格在精度修正后被截断，已丢弃部分订单")
	}

	return filtered
}

func roundToStep(value, step float64) float64 {
	if step <= 0 {
		return value
	}
	return math.Round(value/step) * step
}

func (r *Runner) isEmergencyCooling(symbol string, cooldown time.Duration) bool {
	r.emergencyMu.Lock()
	defer r.emergencyMu.Unlock()
	last, ok := r.lastEmergencyAction[symbol]
	if !ok {
		return false
	}
	return time.Since(last) < cooldown
}

func (r *Runner) markEmergency(symbol string) {
	r.emergencyMu.Lock()
	defer r.emergencyMu.Unlock()
	r.lastEmergencyAction[symbol] = time.Now()
}

func (r *Runner) shouldFallbackReduceOnly(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "ReduceOnly Order is rejected") || strings.Contains(msg, "-2022")
}

func (r *Runner) getEmergencyLimitPrice(symbol, side string, symCfg *config.SymbolConfig) (float64, bool) {
	state := r.store.GetSymbolState(symbol)
	if state == nil || symCfg == nil {
		return 0, false
	}

	state.Mu.RLock()
	bestBid := state.BestBid
	bestAsk := state.BestAsk
	mid := state.MidPrice
	state.Mu.RUnlock()

	tick := symCfg.TickSize
	if tick <= 0 {
		tick = 0.01
	}

	price := 0.0
	if strings.ToUpper(side) == "SELL" {
		if bestBid > 0 {
			price = bestBid - tick*0.5
			if price <= 0 {
				price = bestBid
			}
		} else if mid > 0 {
			price = mid * 0.995
		}
	} else {
		if bestAsk > 0 {
			price = bestAsk + tick*0.5
		} else if mid > 0 {
			price = mid * 1.005
		}
	}

	if price <= 0 {
		return 0, false
	}

	price = math.Round(price/tick) * tick
	if price <= 0 {
		price = tick
	}
	return price, true
}

func (r *Runner) trackEmergencyOrder(symbol, clientID, side string, qty float64, orderType string) {
	if clientID == "" {
		return
	}

	r.emergencyMu.Lock()
	r.emergencyOrders[clientID] = &emergencyOrder{
		Symbol:    symbol,
		Side:      side,
		Quantity:  qty,
		OrderType: orderType,
		CreatedAt: time.Now(),
	}
	r.emergencyMu.Unlock()

	log.Info().
		Str("symbol", symbol).
		Str("side", side).
		Str("client_id", clientID).
		Str("type", orderType).
		Float64("qty", qty).
		Msg("风控追踪：已记录极限清仓订单")
}

func (r *Runner) handleEmergencyOrderUpdate(order *gateway.Order) {
	if order == nil || order.ClientOrderID == "" {
		return
	}

	if !(strings.HasPrefix(order.ClientOrderID, "phoenix-guard-") ||
		strings.HasPrefix(order.ClientOrderID, "phoenix-guard-limit-")) {
		return
	}

	r.emergencyMu.Lock()
	emOrder, ok := r.emergencyOrders[order.ClientOrderID]
	if !ok {
		r.emergencyMu.Unlock()
		log.Warn().
			Str("symbol", order.Symbol).
			Str("client_id", order.ClientOrderID).
			Msg("收到极限风控订单更新，但本地未跟踪该订单")
		return
	}

	finalStatus := order.Status == "FILLED" ||
		order.Status == "CANCELED" ||
		order.Status == "EXPIRED" ||
		order.Status == "REJECTED"
	if finalStatus {
		delete(r.emergencyOrders, order.ClientOrderID)
	}
	r.emergencyMu.Unlock()

	log.Info().
		Str("symbol", order.Symbol).
		Str("client_id", order.ClientOrderID).
		Str("status", order.Status).
		Str("side", order.Side).
		Float64("filled", order.FilledQty).
		Msg("极限风控订单状态更新")

	if finalStatus {
		if order.Status == "FILLED" {
			r.markEmergency(order.Symbol)
			log.Info().
				Str("symbol", order.Symbol).
				Str("client_id", order.ClientOrderID).
				Float64("qty", emOrder.Quantity).
				Msg("极限风控订单已全部成交，冷却计时重置")
		} else {
			r.resetEmergencyCooldown(order.Symbol)
			log.Warn().
				Str("symbol", order.Symbol).
				Str("client_id", order.ClientOrderID).
				Str("status", order.Status).
				Msg("极限风控订单未能成交，已清除冷却限制以便重试")
		}
	}
}

func (r *Runner) resetEmergencyCooldown(symbol string) {
	r.emergencyMu.Lock()
	delete(r.lastEmergencyAction, symbol)
	r.emergencyMu.Unlock()
}

// onDepthUpdate 【P1-1】接收深度更新并发送到channel(非阻塞)
// 这是WebSocket回调,必须快速返回,不能阻塞
func (r *Runner) onDepthUpdate(depth *gateway.Depth) {
	if depth == nil {
		return
	}

	// 【P1-1】非阻塞发送到channel
	select {
	case r.depthChan <- depth:
		// 成功发送

		// 监控channel使用率
		channelLen := len(r.depthChan)
		channelCap := cap(r.depthChan)
		metrics.UpdateDepthChannelMetrics(channelLen, channelCap)
		if channelLen > int(float64(channelCap)*DEPTH_CHANNEL_WARNING_PCT) {
			log.Warn().
				Str("symbol", depth.Symbol).
				Int("channel_len", channelLen).
				Int("channel_cap", channelCap).
				Float64("usage_pct", float64(channelLen)/float64(channelCap)*100).
				Msg("【P1-1】深度channel使用率过高,接近背压")
		}

	default:
		// Channel满了,丢弃这条消息(背压保护)
		totalDrops := atomic.AddInt64(&r.depthDropCount, 1)
		channelCap := cap(r.depthChan)
		metrics.UpdateDepthChannelMetrics(channelCap, channelCap)

		// 每N条丢弃记录一次警告
		if totalDrops%DEPTH_DROP_LOG_INTERVAL == 1 {
			log.Error().
				Str("symbol", depth.Symbol).
				Int64("total_drops", r.depthDropCount).
				Msg("【P1-1】深度channel已满,丢弃消息(背压)")
		}

		// 记录到metrics
		metrics.RecordError("depth_drop", depth.Symbol)

		// 直接兜底刷新mid，避免价格长时间不更新
		r.handleDepthBackpressure(depth)
	}
}

// handleDepthBackpressure 在channel满时执行兜底处理:
// 1. 直接更新mid避免价格过期
// 2. 主动丢弃旧消息，释放空间
func (r *Runner) handleDepthBackpressure(depth *gateway.Depth) {
	if depth != nil && len(depth.Bids) > 0 && len(depth.Asks) > 0 {
		bestBid := depth.Bids[0].Price
		bestAsk := depth.Asks[0].Price
		mid := (bestBid + bestAsk) / 2.0
		r.store.UpdateMidPrice(depth.Symbol, mid, bestBid, bestAsk)
	}

	ch := r.depthChan
	if ch == nil {
		return
	}

	channelCap := cap(ch)
	if channelCap == 0 {
		return
	}

	warningThreshold := int(float64(channelCap) * DEPTH_BACKPRESSURE_PCT)
	drainTarget := int(float64(channelCap) * DEPTH_DRAIN_TARGET_PCT)
	if drainTarget < 1 {
		drainTarget = 1
	}

	channelLen := len(ch)
	if channelLen <= warningThreshold {
		return
	}

	drained := 0
	for len(ch) > drainTarget {
		select {
		case <-ch:
			drained++
		default:
			break
		}
		if len(ch) <= drainTarget {
			break
		}
	}

	if drained > 0 {
		log.Warn().
			Str("symbol", depth.Symbol).
			Int("drained", drained).
			Int("remaining", len(ch)).
			Msg("【P1-1】深度channel出现背压，主动丢弃旧消息")
	}

	metrics.UpdateDepthChannelMetrics(len(ch), channelCap)
}

func (r *Runner) drainDepthChannel(reason string) int {
	ch := r.depthChan
	if ch == nil {
		return 0
	}

	drained := 0
	for {
		select {
		case <-ch:
			drained++
		default:
			if drained > 0 {
				metrics.UpdateDepthChannelMetrics(len(ch), cap(ch))
				log.Warn().
					Int("drained", drained).
					Str("reason", reason).
					Msg("主动清空深度channel以恢复处理速度")
			}
			return drained
		}
	}
}

func (r *Runner) refreshMidPriceFromREST(ctx context.Context, symbol string) {
	if r.exchange == nil {
		return
	}

	childCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	depth, err := r.exchange.GetDepth(childCtx, symbol, 5)
	if err != nil {
		log.Warn().
			Err(err).
			Str("symbol", symbol).
			Msg("REST获取深度失败，无法刷新mid")
		return
	}

	if depth == nil || len(depth.Bids) == 0 || len(depth.Asks) == 0 {
		log.Warn().
			Str("symbol", symbol).
			Msg("REST深度为空，无法刷新mid")
		return
	}

	bestBid := depth.Bids[0].Price
	bestAsk := depth.Asks[0].Price
	if bestBid <= 0 || bestAsk <= 0 {
		return
	}

	mid := (bestBid + bestAsk) / 2.0
	r.store.UpdateMidPrice(symbol, mid, bestBid, bestAsk)

	log.Info().
		Str("symbol", symbol).
		Float64("mid", mid).
		Msg("已通过REST深度刷新mid价")
}

// onOrderUpdate 处理订单更新
func (r *Runner) onOrderUpdate(order *gateway.Order) {
	if order == nil {
		return
	}

	r.handleEmergencyOrderUpdate(order)

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

			// 【修复断流】将检测阈值从5秒降低到2秒，更快检测断流
			// 【关键修复】使用常量配置的阈值
			if !lastUpdate.IsZero() && time.Since(lastUpdate) > time.Duration(STALE_PRICE_THRESHOLD_SECONDS)*time.Second {
				log.Error().
					Str("symbol", symbol).
					Time("last_update", lastUpdate).
					Dur("stale_duration", time.Since(lastUpdate)).
					Float64("last_mid_price", midPrice).
					Msg("【严重告警】深度数据停止更新，WebSocket可能断流！")

				// 记录错误
				metrics.RecordError("websocket_stale", symbol)

				// 【修复断流】触发WebSocket重连（移除防抖，立即重连）
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

// tryReconnectWebSocket 尝试重连WebSocket
// 【P0-3】统一重连状态管理,使用指数退避算法,防止重复重连
func (r *Runner) tryReconnectWebSocket() {
	r.reconnectMu.Lock()

	// 检查是否已经有重连在进行中
	if r.reconnectInProgress {
		r.reconnectMu.Unlock()
		log.Debug().Msg("重连已在进行中，跳过重复请求")
		return
	}

	// 计算指数退避延迟: 2^attempts 秒, 最大64秒
	backoffDelay := time.Duration(1<<uint(r.reconnectAttempts)) * time.Second
	if backoffDelay > 64*time.Second {
		backoffDelay = 64 * time.Second
	}

	// 检查是否需要等待退避时间
	if !r.lastReconnectTime.IsZero() && time.Since(r.lastReconnectTime) < backoffDelay {
		r.reconnectMu.Unlock()
		log.Debug().
			Dur("backoff", backoffDelay).
			Dur("elapsed", time.Since(r.lastReconnectTime)).
			Msg("重连请求被指数退避限制")
		return
	}

	// 标记重连进行中
	r.reconnectInProgress = true
	r.lastReconnectTime = time.Now()
	r.reconnectAttempts++
	currentAttempt := r.reconnectAttempts
	r.reconnectMu.Unlock()

	// 在新 goroutine 中执行重连，避免阻塞
	go func() {
		log.Warn().
			Int("attempt", currentAttempt).
			Dur("backoff", backoffDelay).
			Msg("【P0-3】开始WebSocket重连...")

		ctx := context.Background()
		err := r.exchange.ReconnectStreams(ctx)

		r.reconnectMu.Lock()
		r.reconnectInProgress = false
		if err != nil {
			r.reconnectFailCount++
			log.Error().
				Err(err).
				Int("attempt", currentAttempt).
				Int("fail_count", r.reconnectFailCount).
				Msg("WebSocket重连失败")
			metrics.RecordError("reconnect_fail", "websocket")
		} else {
			// 重连成功,重置计数器
			r.reconnectAttempts = 0
			r.reconnectSuccessCount++
			log.Info().
				Int("success_count", r.reconnectSuccessCount).
				Msg("【P0-3】WebSocket重连成功，计数器已重置")
		}
		r.reconnectMu.Unlock()
	}()
}

// EnterSafeMode 进入安全模式，停止报价等待恢复
func (r *Runner) EnterSafeMode(reason string) {
	r.safeModeMu.Lock()
	defer r.safeModeMu.Unlock()
	if r.safeMode {
		return
	}
	r.safeMode = true
	r.safeModeReason = reason
	log.Warn().Str("reason", reason).Msg("进入安全模式，暂停做市")
}

// ExitSafeMode 退出安全模式
func (r *Runner) ExitSafeMode(reason string) {
	r.safeModeMu.Lock()
	defer r.safeModeMu.Unlock()
	if !r.safeMode {
		return
	}
	prev := r.safeModeReason
	r.safeMode = false
	r.safeModeReason = ""
	log.Info().
		Str("reason", reason).
		Str("previous_reason", prev).
		Msg("退出安全模式，恢复做市")
}

func (r *Runner) inSafeMode() (bool, string) {
	r.safeModeMu.RLock()
	defer r.safeModeMu.RUnlock()
	return r.safeMode, r.safeModeReason
}

// ForceWebSocketReconnect 看门狗触发的WS重连
func (r *Runner) ForceWebSocketReconnect(reason string) {
	log.Warn().
		Str("reason", reason).
		Msg("看门狗触发WebSocket重连")
	r.tryReconnectWebSocket()
}

// ForceResync 强制同步仓位/挂单，与交易所状态对齐
func (r *Runner) ForceResync(reason string) {
	log.Info().Str("reason", reason).Msg("看门狗触发全量状态同步")
	go r.syncExchangeState()
}

func (r *Runner) syncExchangeState() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	positions, err := r.exchange.GetAllPositions(ctx)
	if err != nil {
		log.Error().Err(err).Msg("同步仓位失败")
	} else {
		r.applyPositions(positions)
	}

	for _, symCfg := range r.cfg.Symbols {
		if err := r.om.SyncActiveOrders(ctx, symCfg.Symbol); err != nil {
			log.Error().Err(err).Str("symbol", symCfg.Symbol).Msg("同步活跃订单失败")
		}
	}
}

func (r *Runner) applyPositions(positions []*gateway.Position) {
	for _, pos := range positions {
		if pos == nil {
			continue
		}
		storePos := store.Position{
			Symbol:         pos.Symbol,
			Size:           pos.Size,
			EntryPrice:     pos.EntryPrice,
			UnrealizedPNL:  pos.UnrealizedPNL,
			Notional:       pos.Notional,
			Leverage:       pos.Leverage,
			LastUpdateTime: time.Now(),
		}
		r.store.UpdatePosition(pos.Symbol, storePos)
	}
}
