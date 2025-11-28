package strategy

import (
	"math"
)

// GrindingConfig holds grinding mode configuration
// 文档规范: Grinding 磨仓模式配置
type GrindingConfig struct {
	Enabled        bool    `yaml:"grinding_enabled"`
	Threshold      float64 `yaml:"grinding_thresh"`    // 触发阈值，如 0.4 (40%)
	StdDevThresh   float64 `yaml:"grinding_stddev"`    // 波动率阈值，如 0.0038 (0.38%)
	TakerPercent   float64 `yaml:"grinding_taker_pct"` // Taker比例，如 0.075 (7.5%)
	MakerSpreadBps float64 `yaml:"grinding_maker_bps"` // Maker价差，如 4.2 bps
	SizeMultiplier float64 `yaml:"grinding_size_mult"` // 挂单量倍数，如 2.1
}

// DefaultGrindingConfig returns default grinding configuration
// 文档规范: |pos| >87% + stdDev <0.38%
func DefaultGrindingConfig() GrindingConfig {
	return GrindingConfig{
		Enabled:        true,
		Threshold:      0.87,   // 87% 仓位触发
		StdDevThresh:   0.0038, // 0.38% 波动率
		TakerPercent:   0.075,  // 7.5% taker
		MakerSpreadBps: 4.2,    // 4.2 bps maker
		SizeMultiplier: 2.1,    // 2.1x size
	}
}

// ShouldStartGrinding checks if grinding mode should be activated
// 文档规范: 当仓位适中且市场平静时，采用被动磨仓策略
func (a *ASMM) ShouldStartGrinding(symbol string) bool {
	symCfg := a.cfg.GetSymbolConfig(symbol)
	if symCfg == nil || !symCfg.GrindingEnabled {
		return false
	}

	state := a.store.GetSymbolState(symbol)
	if state == nil {
		return false
	}

	state.Mu.RLock()
	netPosition := state.Position.Size
	state.Mu.RUnlock()

	// 计算仓位比例
	positionRatio := math.Abs(netPosition) / symCfg.NetMax

	// 检查是否超过阈值
	if positionRatio < symCfg.GrindingThresh {
		return false
	}

	// 检查波动率
	stdDev := a.store.PriceStdDev30m(symbol)
	if stdDev >= 0.0038 { // 0.38% 波动率阈值
		return false // 波动太大，不适合grinding
	}

	return true
}

// GenerateGrindingQuotes generates quotes in grinding mode
// 文档规范: Taker 7.5% + Maker reentry @ +4.2bps (size*2.1)
func (a *ASMM) GenerateGrindingQuotes(symbol string, mid float64) ([]Quote, []Quote, error) {
	state := a.store.GetSymbolState(symbol)
	if state == nil {
		return nil, nil, ErrSymbolNotInitialized
	}

	symCfg := a.cfg.GetSymbolConfig(symbol)
	if symCfg == nil {
		return nil, nil, ErrSymbolNotConfigured
	}

	state.Mu.RLock()
	netPosition := state.Position.Size
	state.Mu.RUnlock()

	// 使用固定的grinding配置值
	takerPercent := 0.075 // 7.5%
	makerSpreadBps := 4.2 // 4.2 bps
	sizeMultiplier := 2.1 // 2.1x

	var buyQuotes, sellQuotes []Quote

	// 根据仓位方向决定grinding策略
	if netPosition > 0 {
		// 多头仓位，需要卖出grinding
		// Taker部分: 更激进的卖单
		takerSize := math.Abs(netPosition) * takerPercent
		takerPrice := mid * (1 - 0.0005) // 略低于mid，更容易成交

		sellQuotes = append(sellQuotes, Quote{
			Price: takerPrice,
			Size:  takerSize,
			Layer: 0,
		})

		// Maker部分: 稍高价位的被动卖单
		makerSpread := makerSpreadBps / 10000.0
		makerPrice := mid * (1 + makerSpread)
		makerSize := symCfg.BaseLayerSize * sizeMultiplier

		sellQuotes = append(sellQuotes, Quote{
			Price: makerPrice,
			Size:  makerSize,
			Layer: 1,
		})

		// 买单保持较小规模，避免加仓
		buyPrice := mid * (1 - symCfg.MinSpread)
		buySize := symCfg.BaseLayerSize * 0.5

		buyQuotes = append(buyQuotes, Quote{
			Price: buyPrice,
			Size:  buySize,
			Layer: 0,
		})

	} else if netPosition < 0 {
		// 空头仓位，需要买入grinding
		// Taker部分: 更激进的买单
		takerSize := math.Abs(netPosition) * takerPercent
		takerPrice := mid * (1 + 0.0005) // 略高于mid，更容易成交

		buyQuotes = append(buyQuotes, Quote{
			Price: takerPrice,
			Size:  takerSize,
			Layer: 0,
		})

		// Maker部分: 稍低价位的被动买单
		makerSpread := makerSpreadBps / 10000.0
		makerPrice := mid * (1 - makerSpread)
		makerSize := symCfg.BaseLayerSize * sizeMultiplier

		buyQuotes = append(buyQuotes, Quote{
			Price: makerPrice,
			Size:  makerSize,
			Layer: 1,
		})

		// 卖单保持较小规模，避免加仓
		sellPrice := mid * (1 + symCfg.MinSpread)
		sellSize := symCfg.BaseLayerSize * 0.5

		sellQuotes = append(sellQuotes, Quote{
			Price: sellPrice,
			Size:  sellSize,
			Layer: 0,
		})
	}

	// 舍入价格和数量
	for i := range buyQuotes {
		buyQuotes[i].Price = a.roundPrice(buyQuotes[i].Price, symCfg.TickSize)
		buyQuotes[i].Size = roundSize(buyQuotes[i].Size, symCfg.MinQty)
	}

	for i := range sellQuotes {
		sellQuotes[i].Price = a.roundPrice(sellQuotes[i].Price, symCfg.TickSize)
		sellQuotes[i].Size = roundSize(sellQuotes[i].Size, symCfg.MinQty)
	}

	return buyQuotes, sellQuotes, nil
}

// GetGrindingProgress returns the progress of grinding (0.0 to 1.0)
func (a *ASMM) GetGrindingProgress(symbol string) float64 {
	state := a.store.GetSymbolState(symbol)
	if state == nil {
		return 0
	}

	symCfg := a.cfg.GetSymbolConfig(symbol)
	if symCfg == nil {
		return 0
	}

	state.Mu.RLock()
	netPosition := state.Position.Size
	state.Mu.RUnlock()

	// 仓位比例
	positionRatio := math.Abs(netPosition) / symCfg.NetMax

	grindingThresh := symCfg.GrindingThresh
	if positionRatio < grindingThresh {
		return 0
	}

	// 超过阈值的部分视为需要grinding的进度
	progress := (positionRatio - grindingThresh) / (1.0 - grindingThresh)
	return math.Min(progress, 1.0)
}

// roundSize rounds size to the minimum quantity increment
func roundSize(size, minQty float64) float64 {
	if minQty <= 0 {
		return size
	}
	return math.Round(size/minQty) * minQty
}
