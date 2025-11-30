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

// TestGenerateNormalQuotes_UnifiedGeometricGrid 测试统一几何网格算法
func TestGenerateNormalQuotes_UnifiedGeometricGrid(t *testing.T) {
	cfg := &config.Config{
		Symbols: []config.SymbolConfig{
			{
				Symbol:                "ETHUSDC",
				NetMax:                0.15,
				MinSpread:             0.0007,
				TickSize:              0.01,
				MinQty:                0.001,
				TotalLayers:           18,
				UnifiedLayerSize:      0.0067,
				GridStartOffset:       1.2,
				GridFirstSpacing:      1.2,
				GridSpacingMultiplier: 1.15,
				GridMaxSpacing:        25.0,
				MaxCancelPerMin:       50,
			},
		},
	}

	st := store.NewStore("", time.Hour)
	st.InitSymbol("ETHUSDC", 1800)

	state := st.GetSymbolState("ETHUSDC")
	state.Mu.Lock()
	state.MidPrice = 3000.0
	state.BestBid = 2999.0
	state.BestAsk = 3001.0
	state.Position.Size = 0.0 // 无仓位
	state.Mu.Unlock()

	asmm := NewASMM(cfg, st)

	buyQuotes, sellQuotes, err := asmm.GenerateQuotes(context.Background(), "ETHUSDC")
	if err != nil {
		t.Fatalf("GenerateQuotes failed: %v", err)
	}

	// 验证层数
	if len(buyQuotes) != 18 {
		t.Errorf("Expected 18 buy quotes, got %d", len(buyQuotes))
	}
	if len(sellQuotes) != 18 {
		t.Errorf("Expected 18 sell quotes, got %d", len(sellQuotes))
	}

	// 验证买1距离mid（应该在1.2U左右）
	mid := 3000.0
	buy1Distance := mid - buyQuotes[0].Price
	if buy1Distance < 0.7 || buy1Distance > 1.7 {
		t.Errorf("Buy1 distance %.2f U not in expected range [0.7, 1.7]", buy1Distance)
	}

	// 验证卖1距离mid（应该在1.2U左右）
	sell1Distance := sellQuotes[0].Price - mid
	if sell1Distance < 0.7 || sell1Distance > 1.7 {
		t.Errorf("Sell1 distance %.2f U not in expected range [0.7, 1.7]", sell1Distance)
	}

	// 验证买1买2间距（应该在1.2U左右）
	if len(buyQuotes) >= 2 {
		buy12Spacing := buyQuotes[0].Price - buyQuotes[1].Price
		if buy12Spacing < 0.7 || buy12Spacing > 1.7 {
			t.Errorf("Buy1-2 spacing %.2f U not in expected range [0.7, 1.7]", buy12Spacing)
		}
	}

	// 验证层间距递增
	for i := 2; i < len(buyQuotes) && i < 10; i++ {
		spacing := buyQuotes[i-1].Price - buyQuotes[i].Price
		prevSpacing := buyQuotes[i-2].Price - buyQuotes[i-1].Price
		if spacing <= prevSpacing {
			t.Errorf("Buy layer spacing not increasing at layer %d: %.2f <= %.2f", i, spacing, prevSpacing)
		}
	}

	// 验证最后几层间距不超过25U
	if len(buyQuotes) >= 2 {
		lastIdx := len(buyQuotes) - 1
		lastSpacing := buyQuotes[lastIdx-1].Price - buyQuotes[lastIdx].Price
		if lastSpacing > 26.0 {
			t.Errorf("Last layer spacing %.2f U exceeds max 25U", lastSpacing)
		}
	}

	// 验证订单大小统一
	for i, q := range buyQuotes {
		if q.Size != 0.0067 {
			t.Errorf("Buy quote %d size %.4f != expected 0.0067", i, q.Size)
		}
	}
	for i, q := range sellQuotes {
		if q.Size != 0.0067 {
			t.Errorf("Sell quote %d size %.4f != expected 0.0067", i, q.Size)
		}
	}

	// 打印前5层和后5层供人工检查
	t.Log("=== 前5层买单 ===")
	for i := 0; i < 5 && i < len(buyQuotes); i++ {
		dist := mid - buyQuotes[i].Price
		spacing := 0.0
		if i > 0 {
			spacing = buyQuotes[i-1].Price - buyQuotes[i].Price
		}
		t.Logf("Buy Layer %d: price=%.2f, dist=%.2fU, spacing=%.2fU", i+1, buyQuotes[i].Price, dist, spacing)
	}

	t.Log("=== 后5层买单 ===")
	for i := len(buyQuotes) - 5; i < len(buyQuotes) && i >= 0; i++ {
		dist := mid - buyQuotes[i].Price
		spacing := 0.0
		if i > 0 {
			spacing = buyQuotes[i-1].Price - buyQuotes[i].Price
		}
		t.Logf("Buy Layer %d: price=%.2f, dist=%.2fU, spacing=%.2fU", i+1, buyQuotes[i].Price, dist, spacing)
	}
}

// TestGenerateNormalQuotes_PositionAdjustment 测试仓位调整逻辑
func TestGenerateNormalQuotes_PositionAdjustment(t *testing.T) {
	cfg := &config.Config{
		Symbols: []config.SymbolConfig{
			{
				Symbol:                "ETHUSDC",
				NetMax:                0.15,
				MinSpread:             0.0007,
				TickSize:              0.01,
				MinQty:                0.001,
				TotalLayers:           18,
				UnifiedLayerSize:      0.0067,
				GridStartOffset:       1.2,
				GridFirstSpacing:      1.2,
				GridSpacingMultiplier: 1.15,
				GridMaxSpacing:        25.0,
				MaxCancelPerMin:       50,
			},
		},
	}

	st := store.NewStore("", time.Hour)
	st.InitSymbol("ETHUSDC", 1800)

	state := st.GetSymbolState("ETHUSDC")
	state.Mu.Lock()
	state.MidPrice = 3000.0
	state.BestBid = 2999.0
	state.BestAsk = 3001.0
	state.Position.Size = 0.10 // 多头仓位 (66.7% NetMax)
	state.Mu.Unlock()

	asmm := NewASMM(cfg, st)

	buyQuotes, sellQuotes, err := asmm.GenerateQuotes(context.Background(), "ETHUSDC")
	if err != nil {
		t.Fatalf("GenerateQuotes failed: %v", err)
	}

	// 多头仓位应该减少买单层数
	if len(buyQuotes) >= 18 {
		t.Errorf("Expected buy quotes < 18 with 66.7%% position, got %d", len(buyQuotes))
	}

	// 卖单层数应该保持或略少
	if len(sellQuotes) == 0 {
		t.Errorf("Expected sell quotes > 0, got %d", len(sellQuotes))
	}

	t.Logf("With 66.7%% long position: buy_layers=%d, sell_layers=%d", len(buyQuotes), len(sellQuotes))
}
