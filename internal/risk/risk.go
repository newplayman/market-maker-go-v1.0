package risk

import (
	"fmt"
	"math"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	"github.com/newplayman/market-maker-phoenix/internal/store"
	"github.com/rs/zerolog/log"
)

// Quote 报价结构（从strategy包复制，避免循环依赖）
type Quote struct {
	Price float64
	Size  float64
	Layer int
}

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

	// 【修复1】先检查当前持仓是否已经超标 - 这是最关键的防线
	// 如果持仓已超标，只允许减仓，严格禁止任何开仓操作
	if math.Abs(currentPos) > symCfg.NetMax {
		isReducing := (currentPos > 0 && side == "SELL") || (currentPos < 0 && side == "BUY")
		if !isReducing {
			log.Error().
				Str("symbol", symbol).
				Float64("current_pos", currentPos).
				Float64("net_max", symCfg.NetMax).
				Float64("pos_ratio", math.Abs(currentPos)/symCfg.NetMax*100).
				Str("side", side).
				Float64("size", size).
				Msg("持仓已超标，禁止继续开仓")
			return fmt.Errorf("持仓%.4f已超netMax%.4f(%.1f%%)，禁止继续开仓",
				math.Abs(currentPos), symCfg.NetMax, math.Abs(currentPos)/symCfg.NetMax*100)
		}
		// 仅允许减仓，记录警告日志
		log.Warn().
			Str("symbol", symbol).
			Float64("current_pos", currentPos).
			Float64("net_max", symCfg.NetMax).
			Float64("pos_ratio", math.Abs(currentPos)/symCfg.NetMax*100).
			Str("side", side).
			Float64("size", size).
			Msg("持仓超标，仅允许减仓操作")
	}

	// 检查是否处于Grinding模式
	isGrindingMode := symCfg.GrindingEnabled && math.Abs(currentPos)/symCfg.NetMax > symCfg.GrindingThresh
	isReducingPosition := (currentPos > 0 && side == "SELL") || (currentPos < 0 && side == "BUY")

	// Grinding模式的风控豁免：允许减仓单超过NetMax限制
	if isGrindingMode && isReducingPosition {
		// Grinding减仓单放宽限制到NetMax的120%
		if math.Abs(newPos) > symCfg.NetMax*1.2 {
			return fmt.Errorf("Grinding减仓单净仓位 %.4f 超过宽松限制 %.4f", math.Abs(newPos), symCfg.NetMax*1.2)
		}
		// 通过Grinding减仓检查，跳过后续的正常仓位检查
		log.Debug().
			Str("symbol", symbol).
			Float64("pos", currentPos).
			Str("side", side).
			Float64("size", size).
			Msg("Grinding减仓单通过风控豁免")
	} else {
		// 正常模式：允许减仓单超过限制，但禁止开仓单超过限制，避免死锁
		if math.Abs(newPos) > symCfg.NetMax {
			if (side == "BUY" && newPos > currentPos) || (side == "SELL" && newPos < currentPos) {
				// 这是开仓方向，拒绝
				return fmt.Errorf("净仓位 %.4f 超过限制 %.4f (开仓被拒绝)", math.Abs(newPos), symCfg.NetMax)
			}
			// 减仓方向允许通过
		}
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

// CheckBatchPreTrade 批量检查所有报价的累计风险
// 这是轻仓做市的核心风控：确保所有挂单即使全部成交也不会超过安全限制
func (r *RiskManager) CheckBatchPreTrade(symbol string, buyQuotes, sellQuotes []Quote) error {
	symCfg := r.cfg.GetSymbolConfig(symbol)
	if symCfg == nil {
		return fmt.Errorf("交易对 %s 未配置", symbol)
	}

	state := r.store.GetSymbolState(symbol)
	if state == nil {
		return fmt.Errorf("交易对 %s 未初始化", symbol)
	}

	// 计算当前仓位
	state.Mu.RLock()
	currentPos := state.Position.Size
	state.Mu.RUnlock()

	// 计算所有买单的总量
	totalBuySize := 0.0
	for _, q := range buyQuotes {
		totalBuySize += q.Size
	}

	// 计算所有卖单的总量
	totalSellSize := 0.0
	for _, q := range sellQuotes {
		totalSellSize += q.Size
	}

	// 计算最坏情况：
	// 1. 所有买单成交 -> 仓位变成 currentPos + totalBuySize
	// 2. 所有卖单成交 -> 仓位变成 currentPos - totalSellSize
	worstCaseLong := currentPos + totalBuySize
	worstCaseShort := currentPos - totalSellSize

	// 【关键修复】轻仓做市原则：根据当前仓位方向，只限制加仓方向
	// 原则：
	// - 持有多头时，限制买单（加仓方向），不限制卖单（平仓方向）
	// - 持有空头时，限制卖单（加仓方向），不限制买单（平仓方向）
	// - 无仓位时，双向都限制
	maxWorstCase := symCfg.NetMax * 1.5 // 【修复】从0.5改为1.5，与CheckPreTrade保持一致

	// 根据当前仓位决定检查哪个方向
	if currentPos >= 0 {
		// 无仓位或多头仓位：只检查买单方向（避免加仓过度）
		if math.Abs(worstCaseLong) > maxWorstCase {
			log.Warn().
				Str("symbol", symbol).
				Float64("current_pos", currentPos).
				Float64("total_buy_size", totalBuySize).
				Float64("worst_case_long", worstCaseLong).
				Float64("max_worst_case", maxWorstCase).
				Msg("批量风控：最坏情况多头敞口超限")
			return fmt.Errorf("最坏情况多头敞口 %.4f 超过安全限制 %.4f (当前仓位%.4f + 总买单%.4f)",
				math.Abs(worstCaseLong), maxWorstCase, currentPos, totalBuySize)
		}
		// 卖单方向不检查（平仓方向应允许）
	}

	if currentPos <= 0 {
		// 无仓位或空头仓位：只检查卖单方向（避免加仓过度）
		if math.Abs(worstCaseShort) > maxWorstCase {
			log.Warn().
				Str("symbol", symbol).
				Float64("current_pos", currentPos).
				Float64("total_sell_size", totalSellSize).
				Float64("worst_case_short", worstCaseShort).
				Float64("max_worst_case", maxWorstCase).
				Msg("批量风控：最坏情况空头敞口超限")
			return fmt.Errorf("最坏情况空头敞口 %.4f 超过安全限制 %.4f (当前仓位%.4f - 总卖单%.4f)",
				math.Abs(worstCaseShort), maxWorstCase, currentPos, totalSellSize)
		}
		// 买单方向不检查（平仓方向应允许）
	}

	log.Debug().
		Str("symbol", symbol).
		Float64("current_pos", currentPos).
		Float64("total_buy", totalBuySize).
		Float64("total_sell", totalSellSize).
		Float64("worst_long", worstCaseLong).
		Float64("worst_short", worstCaseShort).
		Float64("max_allowed", maxWorstCase).
		Int("buy_layers", len(buyQuotes)).
		Int("sell_layers", len(sellQuotes)).
		Msg("批量风控检查通过")

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
