package strategy

import (
	"context"
	"math"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	"github.com/newplayman/market-maker-phoenix/internal/store"
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
}

// NewASMM 创建ASMM策略实例
func NewASMM(cfg *config.Config, st *store.Store) *ASMM {
	return &ASMM{
		cfg:   cfg,
		store: st,
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

	// 检查撤单频率
	if err := a.checkCancelRate(state, symCfg); err != nil {
		return nil, nil, err
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

	// 计算库存偏移
	inventorySkew := a.calculateInventorySkew(pos, symCfg.NetMax, mid)

	// 计算资金费率偏移
	fundingBias := a.calculateFundingBias(symbol, mid)

	// 计算波动率调整
	volScaling := a.calculateVolScaling(symbol)

	// 计算reservation价格
	reservation := mid + inventorySkew + fundingBias

	// 计算价差
	spread := symCfg.MinSpread * volScaling * mid

	// 检查是否需要启用钉子模式
	isPinning := symCfg.PinningEnabled && math.Abs(pos)/symCfg.NetMax > symCfg.PinningThresh

	var buyQuotes, sellQuotes []Quote

	if isPinning {
		// 钉子模式：钉在最优价格
		buyQuotes, sellQuotes = a.generatePinningQuotes(symbol, pos, bestBid, bestAsk, symCfg)
	} else {
		// 正常模式：生成多层报价
		buyQuotes, sellQuotes = a.generateNormalQuotes(reservation, spread, symCfg)
	}

	return buyQuotes, sellQuotes, nil
}

// calculateInventorySkew 计算库存偏移
func (a *ASMM) calculateInventorySkew(pos, netMax, mid float64) float64 {
	// 库存比例 [-1, 1]
	inventoryRatio := pos / netMax

	// 偏移系数（可调整）
	skewCoeff := 0.002 // 0.2%

	return -inventoryRatio * skewCoeff * mid
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

// generateNormalQuotes 生成正常模式报价
func (a *ASMM) generateNormalQuotes(reservation, spread float64, cfg *config.SymbolConfig) ([]Quote, []Quote) {
	buyQuotes := make([]Quote, 0, cfg.NearLayers+cfg.FarLayers)
	sellQuotes := make([]Quote, 0, cfg.NearLayers+cfg.FarLayers)

	// 近端层级（动态）
	for i := 0; i < cfg.NearLayers; i++ {
		layer := i + 1
		offset := spread * float64(layer) * 0.5

		buyPrice := reservation - offset
		sellPrice := reservation + offset

		// 价格对齐到tickSize
		buyPrice = a.roundPrice(buyPrice, cfg.TickSize)
		sellPrice = a.roundPrice(sellPrice, cfg.TickSize)

		size := cfg.BaseLayerSize

		buyQuotes = append(buyQuotes, Quote{
			Price: buyPrice,
			Size:  size,
			Layer: layer,
		})

		sellQuotes = append(sellQuotes, Quote{
			Price: sellPrice,
			Size:  size,
			Layer: layer,
		})
	}

	// 远端层级（固定）
	for i := 0; i < cfg.FarLayers; i++ {
		layer := cfg.NearLayers + i + 1

		// 远端价格偏移：±4.8% 到 ±12%
		minOffset := 0.048
		maxOffset := 0.12
		offsetRatio := minOffset + (maxOffset-minOffset)*float64(i)/float64(cfg.FarLayers)

		buyPrice := reservation * (1 - offsetRatio)
		sellPrice := reservation * (1 + offsetRatio)

		buyPrice = a.roundPrice(buyPrice, cfg.TickSize)
		sellPrice = a.roundPrice(sellPrice, cfg.TickSize)

		size := cfg.FarLayerSize

		buyQuotes = append(buyQuotes, Quote{
			Price: buyPrice,
			Size:  size,
			Layer: layer,
		})

		sellQuotes = append(sellQuotes, Quote{
			Price: sellPrice,
			Size:  size,
			Layer: layer,
		})
	}

	return buyQuotes, sellQuotes
}

// generatePinningQuotes 生成钉子模式报价
func (a *ASMM) generatePinningQuotes(symbol string, pos, bestBid, bestAsk float64, cfg *config.SymbolConfig) ([]Quote, []Quote) {
	buyQuotes := make([]Quote, 0)
	sellQuotes := make([]Quote, 0)

	// 钉子大小：基础大小 * 2.3
	pinSize := cfg.BaseLayerSize * 2.3

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
		offsetRatio := 0.048 + 0.072*float64(i)/float64(cfg.FarLayers)

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

// checkCancelRate 检查撤单频率
func (a *ASMM) checkCancelRate(state *store.SymbolState, cfg *config.SymbolConfig) error {
	state.Mu.RLock()
	cancelCount := state.CancelCountLast
	state.Mu.RUnlock()

	// 如果撤单频率超过阈值的80%，返回ErrQuoteFlicker
	if cancelCount >= int(float64(cfg.MaxCancelPerMin)*0.8) {
		return ErrQuoteFlicker
	}

	return nil
}

// roundPrice 价格对齐到tickSize
func (a *ASMM) roundPrice(price, tickSize float64) float64 {
	if tickSize <= 0 {
		return price
	}
	return math.Round(price/tickSize) * tickSize
}

// UpdateMetrics 更新指标
func (a *ASMM) UpdateMetrics() {
	// 由metrics模块处理
}
