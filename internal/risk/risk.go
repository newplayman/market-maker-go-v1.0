package risk

import (
	"fmt"
	"math"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	"github.com/newplayman/market-maker-phoenix/internal/store"
	"github.com/rs/zerolog/log"
)

// RiskManager 风控管理器
type RiskManager struct {
	cfg   *config.Config
	store *store.Store
}

// NewRiskManager 创建风控管理器
func NewRiskManager(cfg *config.Config, st *store.Store) *RiskManager {
	return &RiskManager{
		cfg:   cfg,
		store: st,
	}
}

func (r *RiskManager) CheckPreTrade(symbol string, side string, size float64) error {
	symCfg := r.cfg.GetSymbolConfig(symbol)
	if symCfg == nil {
		return fmt.Errorf("交易对 %s 未配置", symbol)
	}

	state := r.store.GetSymbolState(symbol)
	if state == nil {
		return fmt.Errorf("交易对 %s 未初始化", symbol)
	}

	// 1. 检查单笔订单大小
	if size < symCfg.MinQty {
		return fmt.Errorf("订单量 %.4f 小于最小值 %.4f", size, symCfg.MinQty)
	}

	// 2. 检查净仓位限制，并区分开仓和平仓
	state.Mu.RLock()
	currentPos := state.Position.Size
	state.Mu.RUnlock()

	var newPos float64
	if side == "BUY" {
		newPos = currentPos + size
	} else {
		newPos = currentPos - size
	}

	// 允许减仓单超过限制，但禁止开仓单超过限制，避免死锁
	if math.Abs(newPos) > symCfg.NetMax {
		if (side == "BUY" && newPos > currentPos) || (side == "SELL" && newPos < currentPos) {
			// 这是开仓方向，拒绝
			return fmt.Errorf("净仓位 %.4f 超过限制 %.4f (开仓被拒绝)", math.Abs(newPos), symCfg.NetMax)
		}
		// 减仓方向允许通过
	}

	// 3. 检查最坏情况敞口
	worstCase := r.store.GetWorstCaseLong(symbol)
	var newWorstCase float64
	if side == "BUY" {
		newWorstCase = worstCase + size
	} else {
		newWorstCase = worstCase - size
	}

	if math.Abs(newWorstCase) > symCfg.NetMax*1.5 {
		return fmt.Errorf("最坏情况敞口 %.4f 超过限制 %.4f", math.Abs(newWorstCase), symCfg.NetMax*1.5)
	}

	// 4. 检查总名义价值
	if r.store.IsOverCap(r.cfg.Global.TotalNotionalMax) {
		return fmt.Errorf("总名义价值超过上限 %.2f", r.cfg.Global.TotalNotionalMax)
	}

	// 5. 检查撤单频率
	cancelCount := state.CancelCountLast
	if cancelCount >= symCfg.MaxCancelPerMin {
		return fmt.Errorf("撤单频率过高: %d/min >= %d/min", cancelCount, symCfg.MaxCancelPerMin)
	}

	return nil
}

// CheckStopLoss 检查止损
func (r *RiskManager) CheckStopLoss(symbol string) (shouldStop bool, reason string) {
	symCfg := r.cfg.GetSymbolConfig(symbol)
	if symCfg == nil {
		return false, ""
	}

	state := r.store.GetSymbolState(symbol)
	if state == nil {
		return false, ""
	}

	state.Mu.RLock()
	unrealizedPNL := state.Position.UnrealizedPNL
	notional := state.Position.Notional
	maxDrawdown := state.MaxDrawdown
	state.Mu.RUnlock()

	// 检查未实现亏损
	if notional > 0 {
		lossRatio := math.Abs(unrealizedPNL) / notional
		if unrealizedPNL < 0 && lossRatio > symCfg.StopLossThresh {
			return true, fmt.Sprintf("未实现亏损 %.2f%% 超过止损阈值 %.2f%%",
				lossRatio*100, symCfg.StopLossThresh*100)
		}
	}

	// 检查最大回撤
	if notional > 0 {
		drawdownRatio := maxDrawdown / notional
		if drawdownRatio > symCfg.StopLossThresh*1.5 {
			return true, fmt.Sprintf("最大回撤 %.2f%% 超过阈值 %.2f%%",
				drawdownRatio*100, symCfg.StopLossThresh*1.5*100)
		}
	}

	return false, ""
}

// ShouldReducePosition 是否应该减仓
func (r *RiskManager) ShouldReducePosition(symbol string) (should bool, targetSize float64) {
	symCfg := r.cfg.GetSymbolConfig(symbol)
	if symCfg == nil {
		return false, 0
	}

	state := r.store.GetSymbolState(symbol)
	if state == nil {
		return false, 0
	}

	state.Mu.RLock()
	pos := state.Position.Size
	state.Mu.RUnlock()

	// 如果仓位超过NetMax的80%，建议减仓到50%
	if math.Abs(pos) > symCfg.NetMax*0.8 {
		targetSize = symCfg.NetMax * 0.5
		if pos < 0 {
			targetSize = -targetSize
		}
		return true, targetSize
	}

	return false, 0
}

// ValidateQuotes 验证报价合理性
func (r *RiskManager) ValidateQuotes(symbol string, buyPrice, sellPrice float64) error {
	symCfg := r.cfg.GetSymbolConfig(symbol)
	if symCfg == nil {
		return fmt.Errorf("交易对未配置")
	}

	state := r.store.GetSymbolState(symbol)
	if state == nil {
		return fmt.Errorf("交易对未初始化")
	}

	state.Mu.RLock()
	mid := state.MidPrice
	state.Mu.RUnlock()

	if mid <= 0 {
		return fmt.Errorf("无效的中间价")
	}

	// 检查价差
	spread := (sellPrice - buyPrice) / mid
	if spread < symCfg.MinSpread {
		return fmt.Errorf("价差 %.4f%% 小于最小值 %.4f%%", spread*100, symCfg.MinSpread*100)
	}

	// 检查价格偏离
	buyDeviation := math.Abs(buyPrice-mid) / mid
	sellDeviation := math.Abs(sellPrice-mid) / mid

	maxDeviation := 0.15 // 15%
	if buyDeviation > maxDeviation || sellDeviation > maxDeviation {
		return fmt.Errorf("价格偏离过大: buy=%.2f%%, sell=%.2f%%",
			buyDeviation*100, sellDeviation*100)
	}

	return nil
}

// OnFill 成交后处理
func (r *RiskManager) OnFill(symbol string, side string, size, pnl float64) {
	// 记录成交到store
	r.store.RecordFill(symbol, size, pnl)

	// 检查是否需要启动磨仓
	symCfg := r.cfg.GetSymbolConfig(symbol)
	if symCfg == nil || !symCfg.GrindingEnabled {
		return
	}

	state := r.store.GetSymbolState(symbol)
	if state == nil {
		return
	}

	state.Mu.RLock()
	pos := state.Position.Size
	state.Mu.RUnlock()

	// 如果仓位超过磨仓阈值，触发磨仓
	if math.Abs(pos)/symCfg.NetMax > symCfg.GrindingThresh {
		log.Warn().
			Str("symbol", symbol).
			Float64("pos", pos).
			Float64("thresh", symCfg.GrindingThresh).
			Msg("触发磨仓模式")
	}
}

// CheckGlobal 全局风控检查
func (r *RiskManager) CheckGlobal() error {
	totalNotional := r.store.GetTotalNotional()

	// 检查是否超过全局上限
	if totalNotional > r.cfg.Global.TotalNotionalMax {
		return fmt.Errorf("总名义价值 %.2f 超过上限 %.2f，暂停所有交易",
			totalNotional, r.cfg.Global.TotalNotionalMax)
	}

	return nil
}

// LogRiskMetrics 记录风控指标
func (r *RiskManager) LogRiskMetrics(symbol string) {
	state := r.store.GetSymbolState(symbol)
	if state == nil {
		return
	}

	state.Mu.RLock()
	pos := state.Position.Size
	notional := state.Position.Notional
	unrealizedPNL := state.Position.UnrealizedPNL
	pendingBuy := state.PendingBuy
	pendingSell := state.PendingSell
	state.Mu.RUnlock()

	worstCase := r.store.GetWorstCaseLong(symbol)
	totalNotional := r.store.GetTotalNotional()

	log.Info().
		Str("symbol", symbol).
		Float64("pos", pos).
		Float64("notional", notional).
		Float64("pnl", unrealizedPNL).
		Float64("pending_buy", pendingBuy).
		Float64("pending_sell", pendingSell).
		Float64("worst_case", worstCase).
		Float64("total_notional", totalNotional).
		Msg("风控指标")
}
