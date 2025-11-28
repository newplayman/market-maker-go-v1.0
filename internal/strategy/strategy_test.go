package strategy

import (
	"context"
	"testing"
	"time"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	"github.com/newplayman/market-maker-phoenix/internal/store"
)

func TestASMM_GenerateQuotes(t *testing.T) {
	cfg := &config.Config{
		Symbols: []config.SymbolConfig{
			{
				Symbol:          "BTCUSDT",
				NetMax:          1.0,
				MinSpread:       0.0001,
				BaseLayerSize:   0.01,
				NearLayers:      3,
				FarLayers:       9,
				FarLayerSize:    0.005,
				TickSize:        0.1,
				MinQty:          0.001,
				MaxCancelPerMin: 50,
				PinningEnabled:  true,
				PinningThresh:   0.87,
			},
		},
	}

	st := store.NewStore("", time.Hour) // 测试用1小时快照间隔
	st.InitSymbol("BTCUSDT", 1800)      // 30分钟历史

	// 设置测试数据
	state := st.GetSymbolState("BTCUSDT")
	state.Mu.Lock()
	state.MidPrice = 50000.0
	state.BestBid = 49999.0
	state.BestAsk = 50001.0
	state.CancelCountLast = 0 // 确保撤单计数为0
	state.Mu.Unlock()

	asmm := NewASMM(cfg, st)

	// 测试正常报价生成
	buyQuotes, sellQuotes, err := asmm.GenerateQuotes(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("GenerateQuotes failed: %v", err)
	}

	expectedLayers := cfg.Symbols[0].NearLayers + cfg.Symbols[0].FarLayers
	if len(buyQuotes) != expectedLayers {
		t.Errorf("Expected %d buy quotes, got %d", expectedLayers, len(buyQuotes))
	}
	if len(sellQuotes) != expectedLayers {
		t.Errorf("Expected %d sell quotes, got %d", expectedLayers, len(sellQuotes))
	}

	// 验证价格合理性
	for _, q := range buyQuotes {
		if q.Price >= state.MidPrice {
			t.Errorf("Buy quote price %.2f >= mid price %.2f", q.Price, state.MidPrice)
		}
	}
	for _, q := range sellQuotes {
		if q.Price <= state.MidPrice {
			t.Errorf("Sell quote price %.2f <= mid price %.2f", q.Price, state.MidPrice)
		}
	}
}

func TestASMM_PinningMode(t *testing.T) {
	cfg := &config.Config{
		Symbols: []config.SymbolConfig{
			{
				Symbol:          "BTCUSDT",
				NetMax:          1.0,
				MinSpread:       0.0001,
				BaseLayerSize:   0.01,
				NearLayers:      3,
				FarLayers:       9,
				FarLayerSize:    0.005,
				TickSize:        0.1,
				MaxCancelPerMin: 50,
				PinningEnabled:  true,
				PinningThresh:   0.87,
			},
		},
	}

	st := store.NewStore("", time.Hour)
	st.InitSymbol("BTCUSDT", 1800)

	state := st.GetSymbolState("BTCUSDT")
	state.Mu.Lock()
	state.MidPrice = 50000.0
	state.BestBid = 49999.0
	state.BestAsk = 50001.0
	state.Position.Size = 0.9 // 超过阈值
	state.CancelCountLast = 0 // 确保撤单计数为0
	state.Mu.Unlock()

	asmm := NewASMM(cfg, st)

	_, sellQuotes, err := asmm.GenerateQuotes(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("GenerateQuotes failed: %v", err)
	}

	// 钉子模式应该在最优价格
	if len(sellQuotes) > 0 && sellQuotes[0].Layer == 0 {
		if sellQuotes[0].Price != state.BestAsk {
			t.Errorf("Pinning sell price %.2f != best ask %.2f", sellQuotes[0].Price, state.BestAsk)
		}
	}
}

func TestASMM_CancelRateCheck(t *testing.T) {
	cfg := &config.Config{
		Symbols: []config.SymbolConfig{
			{
				Symbol:          "BTCUSDT",
				NetMax:          1.0,
				MaxCancelPerMin: 50,
				MinSpread:       0.0001,
				BaseLayerSize:   0.01,
				NearLayers:      3,
				FarLayers:       9,
				FarLayerSize:    0.005,
				TickSize:        0.1,
			},
		},
	}

	st := store.NewStore("", time.Hour)
	st.InitSymbol("BTCUSDT", 1800)

	state := st.GetSymbolState("BTCUSDT")
	state.Mu.Lock()
	state.MidPrice = 50000.0
	state.CancelCountLast = 45 // 超过80%阈值
	state.Mu.Unlock()

	asmm := NewASMM(cfg, st)

	_, _, err := asmm.GenerateQuotes(context.Background(), "BTCUSDT")
	if err != ErrQuoteFlicker {
		t.Errorf("Expected ErrQuoteFlicker, got %v", err)
	}
}
