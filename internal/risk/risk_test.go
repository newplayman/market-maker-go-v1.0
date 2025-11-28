package risk

import (
	"testing"
	"time"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	"github.com/newplayman/market-maker-phoenix/internal/store"
)

func TestRiskManager_CheckPreTrade(t *testing.T) {
	cfg := &config.Config{
		Symbols: []config.SymbolConfig{
			{
				Symbol:          "BTCUSDT",
				NetMax:          1.0,
				MinQty:          0.001,
				MaxCancelPerMin: 50,
			},
		},
		Global: config.GlobalConfig{
			TotalNotionalMax: 1000000,
		},
	}

	st := store.NewStore("", time.Hour)
	st.InitSymbol("BTCUSDT", 1800)

	rm := NewRiskManager(cfg, st)

	// 测试正常订单
	err := rm.CheckPreTrade("BTCUSDT", "BUY", 0.05)
	if err != nil {
		t.Errorf("Expected no error for valid order, got %v", err)
	}

	// 测试净仓位超限
	state := st.GetSymbolState("BTCUSDT")
	state.Mu.Lock()
	state.Position.Size = 0.95
	state.Mu.Unlock()

	err = rm.CheckPreTrade("BTCUSDT", "BUY", 0.1)
	if err == nil {
		t.Error("Expected error for position exceeding netMax")
	}
}

func TestRiskManager_CheckStopLoss(t *testing.T) {
	cfg := &config.Config{
		Symbols: []config.SymbolConfig{
			{
				Symbol:          "BTCUSDT",
				StopLossThresh:  0.01, // 1%
				NetMax:          1.0,
				MinSpread:       0.0001,
				MaxCancelPerMin: 50,
			},
		},
	}

	st := store.NewStore("", time.Hour)
	st.InitSymbol("BTCUSDT", 1800)

	rm := NewRiskManager(cfg, st)

	state := st.GetSymbolState("BTCUSDT")
	state.Mu.Lock()
	state.Position.Size = 1.0
	state.Position.Notional = 50000.0
	state.Position.UnrealizedPNL = -600.0 // 亏损1.2%
	state.Mu.Unlock()

	triggered, reason := rm.CheckStopLoss("BTCUSDT")
	if !triggered {
		t.Error("Expected stop loss to be triggered")
	}
	if reason == "" {
		t.Error("Expected stop loss reason")
	}
}

func TestRiskManager_ValidateQuotes(t *testing.T) {
	cfg := &config.Config{
		Symbols: []config.SymbolConfig{
			{
				Symbol:          "BTCUSDT",
				MinSpread:       0.0004, // 0.04%
				MinQty:          0.001,
				MaxCancelPerMin: 50,
			},
		},
	}

	st := store.NewStore("", time.Hour)
	st.InitSymbol("BTCUSDT", 1800)

	state := st.GetSymbolState("BTCUSDT")
	state.Mu.Lock()
	state.MidPrice = 50000.0
	state.Mu.Unlock()

	rm := NewRiskManager(cfg, st)

	// 测试正常报价
	err := rm.ValidateQuotes("BTCUSDT", 49990.0, 50010.0)
	if err != nil {
		t.Errorf("Expected valid quotes, got error: %v", err)
	}

	// 测试价差过小
	err = rm.ValidateQuotes("BTCUSDT", 49999.0, 50001.0)
	if err == nil {
		t.Error("Expected error for spread too narrow")
	}
}

func TestRiskManager_OnFill(t *testing.T) {
	cfg := &config.Config{
		Symbols: []config.SymbolConfig{
			{
				Symbol:          "BTCUSDT",
				NetMax:          1.0,
				GrindingEnabled: true,
				GrindingThresh:  0.8,
			},
		},
	}

	st := store.NewStore("", time.Hour)
	st.InitSymbol("BTCUSDT", 1800)

	rm := NewRiskManager(cfg, st)

	// 模拟成交 - OnFill记录统计信息
	rm.OnFill("BTCUSDT", "BUY", 0.1, 10.0)

	state := st.GetSymbolState("BTCUSDT")
	state.Mu.RLock()
	fillCount := state.FillCount
	totalPNL := state.TotalPNL
	state.Mu.RUnlock()

	if fillCount != 1 {
		t.Errorf("Expected fill count 1, got %d", fillCount)
	}
	if totalPNL != 10.0 {
		t.Errorf("Expected total PNL 10.0, got %.2f", totalPNL)
	}
}
