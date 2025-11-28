package risk

import (
	"fmt"
	"math"
)

// CheckGrindingRisk checks risk limits during grinding mode
// 文档规范: 在grinding模式下额外检查风险
func (rm *RiskManager) CheckGrindingRisk(symbol string) error {
	state := rm.store.GetSymbolState(symbol)
	if state == nil {
		return fmt.Errorf("symbol state not found")
	}

	symCfg := rm.cfg.GetSymbolConfig(symbol)
	if symCfg == nil {
		return fmt.Errorf("symbol config not found")
	}

	state.Mu.RLock()
	netPosition := state.Position.Size
	state.Mu.RUnlock()

	// 计算仓位比例
	positionRatio := math.Abs(netPosition) / symCfg.NetMax

	// 检查是否超过最大仓位比例
	maxRatio := 0.95 // 95%
	if positionRatio > maxRatio {
		return fmt.Errorf("grinding position ratio %.2f%% exceeds limit %.2f%%",
			positionRatio*100, maxRatio*100)
	}

	// 检查是否达到紧急阈值
	emergencyThresh := 0.98 // 98%
	if positionRatio > emergencyThresh {
		return fmt.Errorf("CRITICAL: grinding position ratio %.2f%% exceeds emergency threshold %.2f%%",
			positionRatio*100, emergencyThresh*100)
	}

	// 检查波动率
	stdDev := rm.store.PriceStdDev30m(symbol)
	state.Mu.RLock()
	mid := state.MidPrice
	state.Mu.RUnlock()

	if mid > 0 {
		volRatio := stdDev / mid
		volLimit := 0.005 // 0.5%

		if volRatio > volLimit {
			return fmt.Errorf("grinding volatility %.2f%% exceeds limit %.2f%%",
				volRatio*100, volLimit*100)
		}
	}

	return nil
}

// ShouldPauseGrinding determines if grinding should be paused due to risk
// 文档规范: 风险过高时暂停grinding
func (rm *RiskManager) ShouldPauseGrinding(symbol string) bool {
	// 检查grinding特定风险
	if err := rm.CheckGrindingRisk(symbol); err != nil {
		return true
	}

	// 检查止损
	if shouldStop, _ := rm.CheckStopLoss(symbol); shouldStop {
		return true
	}

	return false
}

// GetGrindingSafetyFactor returns a safety factor (0.0 to 1.0) for grinding
// 值越低，风险越高，grinding应该越保守
func (rm *RiskManager) GetGrindingSafetyFactor(symbol string) float64 {
	state := rm.store.GetSymbolState(symbol)
	if state == nil {
		return 0.0
	}

	symCfg := rm.cfg.GetSymbolConfig(symbol)
	if symCfg == nil {
		return 0.0
	}

	state.Mu.RLock()
	netPosition := state.Position.Size
	unrealizedPNL := state.Position.UnrealizedPNL
	state.Mu.RUnlock()

	// 仓位因子
	positionRatio := math.Abs(netPosition) / symCfg.NetMax
	positionFactor := 1.0 - positionRatio // 仓位越高，因子越低

	// PNL因子
	pnlFactor := 1.0
	if unrealizedPNL < 0 {
		// 亏损时降低安全因子
		notional := math.Abs(netPosition * state.MidPrice)
		if notional > 0 {
			pnlRatio := math.Abs(unrealizedPNL) / notional
			pnlFactor = math.Max(0.5, 1.0-pnlRatio*2) // 亏损越大，因子越低
		}
	}

	// 波动率因子
	stdDev := rm.store.PriceStdDev30m(symbol)
	state.Mu.RLock()
	mid := state.MidPrice
	state.Mu.RUnlock()

	volFactor := 1.0
	if mid > 0 {
		volRatio := stdDev / mid
		volFactor = math.Max(0.5, 1.0-volRatio*100) // 波动越大，因子越低
	}

	// 综合安全因子
	safetyFactor := positionFactor * pnlFactor * volFactor
	return math.Max(0.0, math.Min(1.0, safetyFactor))
}

// AdjustGrindingSize adjusts grinding order size based on risk
// 文档规范: 根据风险动态调整grinding挂单量
func (rm *RiskManager) AdjustGrindingSize(symbol string, baseSize float64) float64 {
	safetyFactor := rm.GetGrindingSafetyFactor(symbol)

	// 安全因子低于0.3时，减少挂单量
	if safetyFactor < 0.3 {
		return baseSize * 0.5 // 减半
	}

	// 安全因子低于0.6时，适度减少
	if safetyFactor < 0.6 {
		return baseSize * 0.75 // 减少25%
	}

	// 正常情况
	return baseSize
}
