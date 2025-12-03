package strategy

import (
	"context"
	"math"
	"sync"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	"github.com/newplayman/market-maker-phoenix/internal/store"
	"github.com/rs/zerolog/log"
)

// Quote 报价
type Quote struct {
	Price float64 // 价格
	Size  float64 // 数量
	Layer int     // 层级
}

// Strategy 策略接口
type Strategy interface {
	// GenerateQuotes 生成买卖报价
	GenerateQuotes(ctx context.Context, symbol string) (buyQuotes []Quote, sellQuotes []Quote, err error)
	// UpdateMetrics 更新指标
	UpdateMetrics()
}

// ASMM Adaptive Skewed Market Making 策略
type ASMM struct {
	cfg   *config.Config
	store *store.Store

	// 状态记忆，用于减少抖动
	mu                  sync.RWMutex
	lastInventoryRatios map[string]float64

	// VPIN支持（可选）
	vpinCalculators map[string]*VPINCalculator // per-symbol VPIN计算器
	vpinMu          sync.RWMutex               // VPIN相关操作的锁
}

// NewASMM 创建ASMM策略实例
func NewASMM(cfg *config.Config, st *store.Store) *ASMM {
	return &ASMM{
		cfg:                 cfg,
		store:               st,
		lastInventoryRatios: make(map[string]float64),
		vpinCalculators:     make(map[string]*VPINCalculator),
	}
}

// GenerateQuotes 生成报价
func (a *ASMM) GenerateQuotes(ctx context.Context, symbol string) ([]Quote, []Quote, error) {
	symCfg := a.cfg.GetSymbolConfig(symbol)
	if symCfg == nil {
		return nil, nil, ErrSymbolNotConfigured
	}

	state := a.store.GetSymbolState(symbol)
	if state == nil {
		return nil, nil, ErrSymbolNotInitialized
	}

	state.Mu.RLock()
	mid := state.MidPrice
	pos := state.Position.Size
	bestBid := state.BestBid
	bestAsk := state.BestAsk
	state.Mu.RUnlock()

	if mid <= 0 {
		return nil, nil, ErrInvalidMidPrice
	}

	// 【修复3】紧急熔断机制：持仓超过80% NetMax时停止报价
	// 这是最后一道防线，防止持仓失控导致强平
	posRatio := math.Abs(pos) / symCfg.NetMax
	if posRatio > 0.80 {
		log.Warn().
			Str("symbol", symbol).
			Float64("pos", pos).
			Float64("net_max", symCfg.NetMax).
			Float64("pos_ratio", posRatio*100).
			Msg("【紧急熔断警告】持仓超过80% netMax，将仅允许减仓")
		// 【修复】不要停止报价，否则无法减仓！
		// 后续的adjustQuotesForRisk会负责过滤掉加仓单
		// return nil, nil, fmt.Errorf("紧急熔断: 持仓使用率%.1f%%超过80%%阈值，停止报价", posRatio*100)
	}

	// 如果持仓超过50%，记录警告日志
	if posRatio > 0.50 {
		log.Warn().
			Str("symbol", symbol).
			Float64("pos", pos).
			Float64("net_max", symCfg.NetMax).
			Float64("pos_ratio", posRatio*100).
			Msg("【风控警告】持仓已超过50% netMax，需要注意风险")
	}

	// 计算库存偏移
	inventorySkew := a.calculateInventorySkew(symbol, pos, symCfg.NetMax, mid, symCfg)

	// 计算资金费率偏移
	fundingBias := a.calculateFundingBias(symbol, mid)

	// 计算波动率调整
	volScaling := a.calculateVolScaling(symbol)

	// 计算reservation价格
	reservation := mid + inventorySkew + fundingBias

	// 计算价差
	spread := symCfg.MinSpread * volScaling * mid

	// 确保价差至少为配置的最小价差，避免违反风控规则
	if spread < symCfg.MinSpread*mid {
		spread = symCfg.MinSpread * mid
	}

	// 【VPIN集成】检查VPIN并调整价差或暂停报价
	vpinValue := a.getVPIN(symbol)
	isGrindingMode := a.ShouldStartGrinding(symbol)

	// VPIN暂停检查（Grinding模式豁免）
	// 根据计划：Grinding > VPIN暂停，确保减仓优先
	if vpinValue >= 0.9 && !isGrindingMode {
		log.Warn().
			Str("symbol", symbol).
			Float64("vpin", vpinValue).
			Float64("pos_ratio", posRatio).
			Msg("【VPIN警报】毒性过高，暂停报价")
		return nil, nil, ErrHighVPINToxicity
	}

	// VPIN价差调整（所有模式都应用）
	if vpinValue >= 0.7 {
		vpinMultiplier := 1.0 + vpinValue*0.2
		spread *= vpinMultiplier

		log.Info().
			Str("symbol", symbol).
			Float64("vpin", vpinValue).
			Float64("spread_multiplier", vpinMultiplier).
			Float64("original_spread", spread/vpinMultiplier).
			Float64("adjusted_spread", spread).
			Msg("【VPIN调整】订单流毒性检测，扩大价差")
	}

	// 模式优先级判断：Grinding > Pinning > Normal
	// Grinding优先级最高（仓位最危险，需要主动减仓）
	// Pinning次之（仓位较大，需要被动等待）
	// Normal为默认模式

	var buyQuotes, sellQuotes []Quote
	var err error
	var mode string

	if isGrindingMode {
		// Grinding模式：主动减仓
		mode = "grinding"
		buyQuotes, sellQuotes, err = a.GenerateGrindingQuotes(symbol, mid)
		if err != nil {
			return nil, nil, err
		}
	} else if symCfg.PinningEnabled && math.Abs(pos)/symCfg.NetMax > symCfg.PinningThresh {
		// Pinning模式：钉在最优价格
		mode = "pinning"
		buyQuotes, sellQuotes = a.generatePinningQuotes(bestBid, bestAsk, pos, symCfg)
	} else {
		// 正常模式：生成多层报价
		mode = "normal"
		buyQuotes, sellQuotes = a.generateNormalQuotes(reservation, spread, symCfg)
	}

	// 记录模式切换（仅在模式变化时记录）
	a.logModeChange(symbol, mode, pos, symCfg.NetMax)

	return buyQuotes, sellQuotes, nil
}

// calculateInventorySkew 计算库存偏移
func (a *ASMM) calculateInventorySkew(symbol string, pos, netMax, mid float64, cfg *config.SymbolConfig) float64 {
	// 库存比例 [-1, 1]
	currentRatio := pos / netMax

	// 获取上一次的比例
	a.mu.RLock()
	lastRatio, exists := a.lastInventoryRatios[symbol]
	a.mu.RUnlock()

	targetRatio := currentRatio
	// 死区逻辑：如果变化小于 5%，则保持上一次的比例 (仅当上一次存在时)
	// 但如果仓位反向了（比如从正变负），则立即更新
	if exists {
		diff := math.Abs(currentRatio - lastRatio)
		if diff < 0.05 && (currentRatio*lastRatio >= 0) {
			targetRatio = lastRatio
		}
	}

	// 更新记录
	if targetRatio != lastRatio {
		a.mu.Lock()
		a.lastInventoryRatios[symbol] = targetRatio
		a.mu.Unlock()
	}

	// 使用配置的偏移系数，如果未配置则使用默认值0.002
	skewCoeff := 0.002
	if cfg.InventorySkewCoeff > 0 {
		skewCoeff = cfg.InventorySkewCoeff
	}

	return -targetRatio * skewCoeff * mid
}

// calculateFundingBias 计算资金费率偏移
func (a *ASMM) calculateFundingBias(symbol string, mid float64) float64 {
	predictedFunding := a.store.PredictedFunding(symbol)

	// 资金费率偏移系数
	fundingCoeff := 0.5

	return -predictedFunding * fundingCoeff * mid
}

// calculateVolScaling 计算波动率调整系数
func (a *ASMM) calculateVolScaling(symbol string) float64 {
	stdDev := a.store.PriceStdDev30m(symbol)
	state := a.store.GetSymbolState(symbol)

	if state == nil {
		return 1.0
	}

	state.Mu.RLock()
	mid := state.MidPrice
	state.Mu.RUnlock()

	if mid <= 0 {
		return 1.0
	}

	// 波动率比例
	volRatio := stdDev / mid

	// 基础系数1.0，波动率每增加0.1%，系数增加0.05
	scaling := 1.0 + volRatio*50

	// 限制在[0.8, 2.0]范围
	if scaling < 0.8 {
		scaling = 0.8
	}
	if scaling > 2.0 {
		scaling = 2.0
	}

	return scaling
}

// generateNormalQuotes 生成正常模式报价 - 统一几何网格算法
func (a *ASMM) generateNormalQuotes(reservation, spread float64, cfg *config.SymbolConfig) ([]Quote, []Quote) {
	// 获取当前仓位
	state := a.store.GetSymbolState(cfg.Symbol)
	var currentPos float64
	if state != nil {
		state.Mu.RLock()
		currentPos = state.Position.Size
		state.Mu.RUnlock()
	}

	// 计算仓位比例
	posRatio := math.Abs(currentPos) / cfg.NetMax

	// 【用户选择】订单大小：所有层统一大小
	orderSize := cfg.UnifiedLayerSize
	if orderSize <= 0 {
		// 兼容旧配置
		orderSize = cfg.BaseLayerSize
	}
	if orderSize < cfg.MinQty {
		orderSize = cfg.MinQty
	}

	// 【方向性调整】如果有仓位，减少加仓方向的层数
	buyLayerCount := cfg.TotalLayers
	sellLayerCount := cfg.TotalLayers

	// 【调试】记录层数配置
	log.Info().
		Str("symbol", cfg.Symbol).
		Int("total_layers", cfg.TotalLayers).
		Int("near_layers", cfg.NearLayers).
		Int("far_layers", cfg.FarLayers).
		Float64("unified_layer_size", cfg.UnifiedLayerSize).
		Float64("base_layer_size", cfg.BaseLayerSize).
		Msg("几何网格配置参数")

	if buyLayerCount == 0 {
		// 兼容旧配置
		buyLayerCount = cfg.NearLayers + cfg.FarLayers
		sellLayerCount = cfg.NearLayers + cfg.FarLayers
	}

	// 【关键修复】仓位方向调整逻辑
	// 原则：持仓时，减少加仓方向订单，增加平仓方向订单
	// 目的：便于平仓获利，避免继续加仓增加风险
	if currentPos > 0 {
		// 多头仓位：减少买单（避免加仓），保持或略增卖单（便于平仓）
		buyLayerCount = int(float64(buyLayerCount) * (1.0 - posRatio*0.6))
		if buyLayerCount < 1 {
			buyLayerCount = 1
		}
		// 卖单保持满层数，不减少（确保有足够卖单平仓）
		// 不额外增加，因为批量风控会根据仓位调整
	} else if currentPos < 0 {
		// 空头仓位：减少卖单（避免加仓），保持或略增买单（便于平仓）
		sellLayerCount = int(float64(sellLayerCount) * (1.0 - posRatio*0.6))
		if sellLayerCount < 1 {
			sellLayerCount = 1
		}
		// 买单保持满层数，不减少（确保有足够买单平仓）
	}

	buyQuotes := make([]Quote, 0, buyLayerCount)
	sellQuotes := make([]Quote, 0, sellLayerCount)

	// 【统一几何网格算法】
	// 检查是否配置了新的几何网格参数
	useUnifiedGrid := cfg.GridStartOffset > 0 && cfg.GridFirstSpacing > 0 && cfg.GridSpacingMultiplier > 1.0

	if useUnifiedGrid {
		// 使用新的统一几何网格算法
		// 公式：第n层距离mid = GridStartOffset + Σ(GridFirstSpacing × GridSpacingMultiplier^i), i=0 to n-1
		// 即：第1层距离mid = GridStartOffset
		//     第2层距离mid = GridStartOffset + GridFirstSpacing
		//     第3层距离mid = GridStartOffset + GridFirstSpacing + GridFirstSpacing × multiplier
		//     第n层距离mid = GridStartOffset + GridFirstSpacing × (1 + multiplier + multiplier^2 + ... + multiplier^(n-2))

		// 买单
		for i := 0; i < buyLayerCount; i++ {
			layer := i + 1

			// 计算距离mid的总距离（USDT）
			var distanceFromMid float64
			if i == 0 {
				// 第1层：仅初始偏移
				distanceFromMid = cfg.GridStartOffset
			} else {
				// 第2层及以后：初始偏移 + 累计层间距
				distanceFromMid = cfg.GridStartOffset
				for j := 0; j < i; j++ {
					spacing := cfg.GridFirstSpacing * math.Pow(cfg.GridSpacingMultiplier, float64(j))

					// 限制最大层间距
					if cfg.GridMaxSpacing > 0 && spacing > cfg.GridMaxSpacing {
						spacing = cfg.GridMaxSpacing
					}

					distanceFromMid += spacing
				}
			}

			// 转换为价格（买单在reservation下方）
			buyPrice := reservation - distanceFromMid
			buyPrice = a.roundPrice(buyPrice, cfg.TickSize)

			buyQuotes = append(buyQuotes, Quote{
				Price: buyPrice,
				Size:  orderSize,
				Layer: layer,
			})
		}

		// 卖单（对称）
		for i := 0; i < sellLayerCount; i++ {
			layer := i + 1

			var distanceFromMid float64
			if i == 0 {
				distanceFromMid = cfg.GridStartOffset
			} else {
				distanceFromMid = cfg.GridStartOffset
				for j := 0; j < i; j++ {
					spacing := cfg.GridFirstSpacing * math.Pow(cfg.GridSpacingMultiplier, float64(j))
					if cfg.GridMaxSpacing > 0 && spacing > cfg.GridMaxSpacing {
						spacing = cfg.GridMaxSpacing
					}
					distanceFromMid += spacing
				}
			}

			sellPrice := reservation + distanceFromMid
			sellPrice = a.roundPrice(sellPrice, cfg.TickSize)

			sellQuotes = append(sellQuotes, Quote{
				Price: sellPrice,
				Size:  orderSize,
				Layer: layer,
			})
		}

		// 记录网格信息
		if len(buyQuotes) > 0 && len(sellQuotes) > 0 {
			log.Debug().
				Str("symbol", cfg.Symbol).
				Float64("pos", currentPos).
				Float64("pos_ratio", posRatio).
				Int("buy_layers", len(buyQuotes)).
				Int("sell_layers", len(sellQuotes)).
				Float64("buy1_price", buyQuotes[0].Price).
				Float64("sell1_price", sellQuotes[0].Price).
				Float64("buy1_distance", reservation-buyQuotes[0].Price).
				Float64("sell1_distance", sellQuotes[0].Price-reservation).
				Msg("生成统一几何网格报价")
		}
	} else {
		// 使用旧的near/far分层算法（兼容性）
		buyQuotes, sellQuotes = a.generateLegacyQuotes(reservation, spread, cfg, buyLayerCount, sellLayerCount, orderSize, posRatio)
	}

	return buyQuotes, sellQuotes
}

// generateLegacyQuotes 生成旧版分层报价（兼容性函数）
func (a *ASMM) generateLegacyQuotes(reservation, spread float64, cfg *config.SymbolConfig,
	buyLayerCount, sellLayerCount int, orderSize, posRatio float64) ([]Quote, []Quote) {

	effectiveBuyNearLayers := cfg.NearLayers
	effectiveSellNearLayers := cfg.NearLayers
	effectiveBuyFarLayers := cfg.FarLayers
	effectiveSellFarLayers := cfg.FarLayers

	// 根据层数限制调整
	if buyLayerCount < effectiveBuyNearLayers+effectiveBuyFarLayers {
		ratio := float64(buyLayerCount) / float64(effectiveBuyNearLayers+effectiveBuyFarLayers)
		effectiveBuyNearLayers = int(float64(cfg.NearLayers) * ratio)
		effectiveBuyFarLayers = buyLayerCount - effectiveBuyNearLayers
	}
	if sellLayerCount < effectiveSellNearLayers+effectiveSellFarLayers {
		ratio := float64(sellLayerCount) / float64(effectiveSellNearLayers+effectiveSellFarLayers)
		effectiveSellNearLayers = int(float64(cfg.NearLayers) * ratio)
		effectiveSellFarLayers = sellLayerCount - effectiveSellNearLayers
	}

	buyQuotes := make([]Quote, 0, buyLayerCount)
	sellQuotes := make([]Quote, 0, sellLayerCount)

	// 近端层级（动态）- 使用配置的起始偏移和几何公比
	// 买单近端层
	for i := 0; i < effectiveBuyNearLayers; i++ {
		layer := i + 1

		// 使用配置的参数，如果未配置则使用默认值
		startOffset := cfg.NearLayerStartOffset
		if startOffset <= 0 {
			startOffset = 0.00033 // 默认0.033% (约1U @ 3000U价格)
		}

		spacingRatio := cfg.NearLayerSpacingRatio
		if spacingRatio <= 1.0 {
			spacingRatio = 1.15 // 默认几何公比1.15
		}

		// 计算第i层的偏移比例: startOffset * ratio^i
		offsetRatio := startOffset * math.Pow(spacingRatio, float64(i))

		buyPrice := reservation * (1 - offsetRatio)

		// 价格对齐到tickSize
		buyPrice = a.roundPrice(buyPrice, cfg.TickSize)

		buyQuotes = append(buyQuotes, Quote{
			Price: buyPrice,
			Size:  orderSize,
			Layer: layer,
		})
	}

	// 卖单近端层
	for i := 0; i < effectiveSellNearLayers; i++ {
		layer := i + 1

		startOffset := cfg.NearLayerStartOffset
		if startOffset <= 0 {
			startOffset = 0.00033
		}

		spacingRatio := cfg.NearLayerSpacingRatio
		if spacingRatio <= 1.0 {
			spacingRatio = 1.15
		}

		offsetRatio := startOffset * math.Pow(spacingRatio, float64(i))

		sellPrice := reservation * (1 + offsetRatio)

		sellPrice = a.roundPrice(sellPrice, cfg.TickSize)

		sellQuotes = append(sellQuotes, Quote{
			Price: sellPrice,
			Size:  orderSize,
			Layer: layer,
		})
	}

	// 远端层级（固定）- 使用配置的起始和结束偏移
	// 买单远端层
	for i := 0; i < effectiveBuyFarLayers; i++ {
		layer := effectiveBuyNearLayers + i + 1

		// 使用配置的参数，如果未配置则使用默认值
		startOffset := cfg.FarLayerStartOffset
		if startOffset <= 0 {
			startOffset = 0.0067 // 默认0.67% (约20U @ 3000U价格)
		}

		endOffset := cfg.FarLayerEndOffset
		if endOffset <= 0 {
			endOffset = 0.02 // 默认2% (约60U @ 3000U价格)
		}

		// 根据配置决定使用几何增长还是线性增长
		var offsetRatio float64
		if cfg.LayerSpacingMode == "geometric" && cfg.SpacingRatio > 1.0 {
			// 使用几何增长模式
			// 公比计算: r = (end/start)^(1/(n-1))
			// 第n层偏移 = start * r^(n-1)
			if effectiveBuyFarLayers > 1 {
				ratio := math.Pow(endOffset/startOffset, 1.0/float64(effectiveBuyFarLayers-1))
				offsetRatio = startOffset * math.Pow(ratio, float64(i))
			} else {
				offsetRatio = startOffset
			}
		} else {
			// 默认使用线性增长模式
			offsetRatio = startOffset + (endOffset-startOffset)*float64(i)/float64(effectiveBuyFarLayers)
		}

		buyPrice := reservation * (1 - offsetRatio)

		buyPrice = a.roundPrice(buyPrice, cfg.TickSize)

		farSize := cfg.FarLayerSize
		if farSize <= 0 {
			farSize = orderSize
		}
		if farSize < cfg.MinQty {
			farSize = cfg.MinQty
		}

		buyQuotes = append(buyQuotes, Quote{
			Price: buyPrice,
			Size:  farSize,
			Layer: layer,
		})
	}

	// 卖单远端层
	for i := 0; i < effectiveSellFarLayers; i++ {
		layer := effectiveSellNearLayers + i + 1

		startOffset := cfg.FarLayerStartOffset
		if startOffset <= 0 {
			startOffset = 0.0067
		}

		endOffset := cfg.FarLayerEndOffset
		if endOffset <= 0 {
			endOffset = 0.02
		}

		var offsetRatio float64
		if cfg.LayerSpacingMode == "geometric" && cfg.SpacingRatio > 1.0 {
			if effectiveSellFarLayers > 1 {
				ratio := math.Pow(endOffset/startOffset, 1.0/float64(effectiveSellFarLayers-1))
				offsetRatio = startOffset * math.Pow(ratio, float64(i))
			} else {
				offsetRatio = startOffset
			}
		} else {
			offsetRatio = startOffset + (endOffset-startOffset)*float64(i)/float64(effectiveSellFarLayers)
		}

		sellPrice := reservation * (1 + offsetRatio)

		sellPrice = a.roundPrice(sellPrice, cfg.TickSize)

		farSize := cfg.FarLayerSize
		if farSize <= 0 {
			farSize = orderSize
		}
		if farSize < cfg.MinQty {
			farSize = cfg.MinQty
		}

		sellQuotes = append(sellQuotes, Quote{
			Price: sellPrice,
			Size:  farSize,
			Layer: layer,
		})
	}

	// 记录调整信息
	if posRatio > 0.1 { // 只在仓位>10%时记录
		log.Debug().
			Str("symbol", cfg.Symbol).
			Float64("pos_ratio", posRatio).
			Int("buy_layers", len(buyQuotes)).
			Int("sell_layers", len(sellQuotes)).
			Msg("根据仓位动态调整挂单（兼容模式）")
	}

	return buyQuotes, sellQuotes
}

// generatePinningQuotes 生成钉子模式报价
func (a *ASMM) generatePinningQuotes(bestBid, bestAsk float64, pos float64, cfg *config.SymbolConfig) ([]Quote, []Quote) {
	buyQuotes := make([]Quote, 0)
	sellQuotes := make([]Quote, 0)

	// 钉子大小：基础大小 * 2.3
	baseSize := cfg.BaseLayerSize
	if baseSize <= 0 {
		baseSize = cfg.UnifiedLayerSize
	}
	pinSize := baseSize * 2.3
	pinSize = a.roundQty(pinSize, cfg.MinQty)

	if pos > 0 {
		// 多头仓位：钉在卖价
		sellQuotes = append(sellQuotes, Quote{
			Price: bestAsk,
			Size:  pinSize,
			Layer: 0,
		})
	} else if pos < 0 {
		// 空头仓位：钉在买价
		buyQuotes = append(buyQuotes, Quote{
			Price: bestBid,
			Size:  pinSize,
			Layer: 0,
		})
	}

	// 添加远端保护层
	mid := (bestBid + bestAsk) / 2
	for i := 0; i < cfg.FarLayers; i++ {
		layer := i + 1

		// 根据配置决定使用几何增长还是线性增长
		var offsetRatio float64
		if cfg.LayerSpacingMode == "geometric" && cfg.SpacingRatio > 1.0 {
			// 使用几何增长模式
			// 起始偏移: 0.048 (4.8%)
			// 结束偏移: 0.12 (12%)
			minOffset := 0.048
			maxOffset := 0.12
			// 公比计算: r = (end/start)^(1/(n-1))
			// 第n层偏移 = start * r^(n-1)
			if cfg.FarLayers > 1 {
				ratio := math.Pow(maxOffset/minOffset, 1.0/float64(cfg.FarLayers-1))
				offsetRatio = minOffset * math.Pow(ratio, float64(i))
			} else {
				offsetRatio = minOffset
			}
		} else {
			// 默认使用线性增长模式
			offsetRatio = 0.048 + 0.072*float64(i)/float64(cfg.FarLayers)
		}

		buyPrice := a.roundPrice(mid*(1-offsetRatio), cfg.TickSize)
		sellPrice := a.roundPrice(mid*(1+offsetRatio), cfg.TickSize)

		buyQuotes = append(buyQuotes, Quote{
			Price: buyPrice,
			Size:  cfg.FarLayerSize,
			Layer: layer,
		})

		sellQuotes = append(sellQuotes, Quote{
			Price: sellPrice,
			Size:  cfg.FarLayerSize,
			Layer: layer,
		})
	}

	return buyQuotes, sellQuotes
}

// roundPrice 价格对齐到tickSize
func (a *ASMM) roundPrice(price, tickSize float64) float64 {
	if tickSize <= 0 {
		return price
	}
	return math.Round(price/tickSize) * tickSize
}

// roundQty 数量对齐到stepSize
func (a *ASMM) roundQty(qty, stepSize float64) float64 {
	if stepSize <= 0 {
		return qty
	}
	// 使用Round确保最接近，避免精度误差
	return math.Round(qty/stepSize) * stepSize
}

// logModeChange 记录模式切换
func (a *ASMM) logModeChange(symbol, mode string, pos, netMax float64) {
	state := a.store.GetSymbolState(symbol)
	if state == nil {
		return
	}

	state.Mu.Lock()
	lastMode := state.LastMode
	if lastMode != mode {
		state.LastMode = mode
		state.Mu.Unlock()

		// 仅在模式变化时记录日志
		posRatio := math.Abs(pos) / netMax
		log.Info().
			Str("symbol", symbol).
			Str("mode", mode).
			Str("prev_mode", lastMode).
			Float64("pos", pos).
			Float64("pos_ratio", posRatio).
			Msg("策略模式切换")
	} else {
		state.Mu.Unlock()
	}
}

// UpdateMetrics 更新指标
func (a *ASMM) UpdateMetrics() {
	// 由metrics模块处理
}

// EnableVPIN 为指定symbol启用VPIN
func (a *ASMM) EnableVPIN(symbol string, cfg VPINConfig) {
	a.vpinMu.Lock()
	defer a.vpinMu.Unlock()

	// 创建VPIN计算器
	calc := NewVPINCalculator(symbol, cfg)
	a.vpinCalculators[symbol] = calc

	// 注册到Store
	a.store.EnableVPIN(symbol, calc)

	log.Info().
		Str("symbol", symbol).
		Float64("bucket_size", cfg.BucketSize).
		Float64("threshold", cfg.Threshold).
		Msg("VPIN已启用")
}

// DisableVPIN 为指定symbol禁用VPIN
func (a *ASMM) DisableVPIN(symbol string) {
	a.vpinMu.Lock()
	defer a.vpinMu.Unlock()

	delete(a.vpinCalculators, symbol)
	a.store.DisableVPIN(symbol)

	log.Info().Str("symbol", symbol).Msg("VPIN已禁用")
}

// getVPIN 获取指定symbol的VPIN值（内部方法）
func (a *ASMM) getVPIN(symbol string) float64 {
	a.vpinMu.RLock()
	calc, exists := a.vpinCalculators[symbol]
	a.vpinMu.RUnlock()

	if !exists || calc == nil {
		return 0.5 // 未启用时返回中性值
	}

	return calc.GetVPIN()
}

// GetVPINStats 获取VPIN统计信息（供外部调用）
func (a *ASMM) GetVPINStats(symbol string) *VPINStats {
	a.vpinMu.RLock()
	calc, exists := a.vpinCalculators[symbol]
	a.vpinMu.RUnlock()

	if !exists || calc == nil {
		return nil
	}

	stats := calc.GetStats()
	return &stats
}

// GetVPINCalculator 获取VPIN计算器（供测试使用）
func (a *ASMM) GetVPINCalculator(symbol string) *VPINCalculator {
	a.vpinMu.RLock()
	defer a.vpinMu.RUnlock()
	return a.vpinCalculators[symbol]
}
