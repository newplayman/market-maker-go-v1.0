package test

import (
	"context"
	"testing"
	"time"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	"github.com/newplayman/market-maker-phoenix/internal/store"
	"github.com/newplayman/market-maker-phoenix/internal/strategy"
)

// TestVPINIntegration_SpreadAdjustment 测试VPIN引起的价差调整
func TestVPINIntegration_SpreadAdjustment(t *testing.T) {
	// 创建测试配置
	cfg := &config.Config{
		Global: config.GlobalConfig{
			TotalNotionalMax: 10000,
		},
		Symbols: []config.SymbolConfig{
			{
				Symbol:                "ETHUSDC",
				NetMax:                1.0,
				MinSpread:             0.001, // 0.1%
				TickSize:              0.01,
				MinQty:                0.01,
				TotalLayers:           5,
				UnifiedLayerSize:      0.1,
				GridStartOffset:       1.0,
				GridFirstSpacing:      1.0,
				GridSpacingMultiplier: 1.15,
				VPINEnabled:           true,
				VPINBucketSize:        10000,
				VPINNumBuckets:        10,
				VPINThreshold:         0.7,
				VPINPauseThresh:       0.9,
				VPINMultiplier:        0.2,
				VPINVolThreshold:      10000,
			},
		},
	}

	// 创建Store
	st := store.NewStore("/tmp/test_vpin.json", 60*time.Second)
	defer st.Close()

	st.InitSymbol("ETHUSDC", 100)

	// 初始化价格
	st.UpdateMidPrice("ETHUSDC", 3000.0, 2999.5, 3000.5)

	// 创建策略
	asmm := strategy.NewASMM(cfg, st)

	// 启用VPIN
	vpinCfg := strategy.VPINConfig{
		BucketSize:   10000,
		NumBuckets:   10,
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 10000,
	}
	asmm.EnableVPIN("ETHUSDC", vpinCfg)

	// 获取VPIN计算器并注入毒性流
	stats := asmm.GetVPINStats("ETHUSDC")
	if stats == nil {
		t.Fatal("VPIN stats should not be nil after enabling")
	}

	// 记录未触发VPIN放大前的基础spread
	baseBuy, baseSell, err := asmm.GenerateQuotes(context.Background(), "ETHUSDC")
	if err != nil {
		t.Fatalf("预热报价失败: %v", err)
	}
	baseSpread := (baseSell[0].Price - baseBuy[0].Price) / 3000.0

	// 模拟注入80%买盘，20%卖盘的毒性流
	// 填充10个buckets，每个bucket 10000成交量
	vpinCalc := asmm.GetVPINCalculator("ETHUSDC")
	if vpinCalc == nil {
		t.Fatal("VPIN calculator should not be nil")
	}

	for i := 0; i < 10; i++ {
		// 80%买盘
		for j := 0; j < 80; j++ {
			trade := strategy.Trade{
				Symbol:    "ETHUSDC",
				Price:     3000.1,
				Quantity:  100,
				Timestamp: time.Now(),
			}
			vpinCalc.UpdateMidPrice(3000.0)
			vpinCalc.UpdateTrade(trade)
		}

		// 20%卖盘
		for j := 0; j < 20; j++ {
			trade := strategy.Trade{
				Symbol:    "ETHUSDC",
				Price:     2999.9,
				Quantity:  100,
				Timestamp: time.Now(),
			}
			vpinCalc.UpdateTrade(trade)
		}
	}

	// 验证VPIN值
	stats = asmm.GetVPINStats("ETHUSDC")
	if stats.VPIN < 0.55 || stats.VPIN > 0.65 {
		t.Errorf("Expected VPIN around 0.6, got %.2f", stats.VPIN)
	}

	t.Logf("VPIN value: %.4f", stats.VPIN)

	// 生成报价，验证spread被放大
	ctx := context.Background()
	buyQuotes, sellQuotes, err := asmm.GenerateQuotes(ctx, "ETHUSDC")

	if err != nil {
		t.Fatalf("GenerateQuotes failed: %v", err)
	}

	if len(buyQuotes) == 0 || len(sellQuotes) == 0 {
		t.Fatal("Expected non-empty quotes")
	}

	// 计算实际spread
	firstBuyPrice := buyQuotes[0].Price
	firstSellPrice := sellQuotes[0].Price
	actualSpread := (firstSellPrice - firstBuyPrice) / 3000.0

	if actualSpread < baseSpread {
		t.Errorf("VPIN开启后spread不应缩小，基础%.4f，实际%.4f", baseSpread, actualSpread)
	}

	t.Logf("Actual spread: %.4f vs base %.4f", actualSpread, baseSpread)
}

// TestVPINIntegration_PauseMechanism 测试VPIN暂停机制
func TestVPINIntegration_PauseMechanism(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			TotalNotionalMax: 10000,
		},
		Symbols: []config.SymbolConfig{
			{
				Symbol:                "ETHUSDC",
				NetMax:                1.0,
				MinSpread:             0.001,
				TickSize:              0.01,
				MinQty:                0.01,
				TotalLayers:           5,
				UnifiedLayerSize:      0.1,
				GridStartOffset:       1.0,
				GridFirstSpacing:      1.0,
				GridSpacingMultiplier: 1.15,
				VPINEnabled:           true,
				VPINBucketSize:        10000,
				VPINNumBuckets:        10,
				VPINThreshold:         0.7,
				VPINPauseThresh:       0.9,
				VPINMultiplier:        0.2,
				VPINVolThreshold:      10000,
			},
		},
	}

	st := store.NewStore("/tmp/test_vpin_pause.json", 60*time.Second)
	defer st.Close()

	st.InitSymbol("ETHUSDC", 100)
	st.UpdateMidPrice("ETHUSDC", 3000.0, 2999.5, 3000.5)

	asmm := strategy.NewASMM(cfg, st)

	vpinCfg := strategy.VPINConfig{
		BucketSize:   10000,
		NumBuckets:   10,
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 10000,
	}
	asmm.EnableVPIN("ETHUSDC", vpinCfg)

	// 注入95%买盘，5%卖盘的极端毒性流
	vpinCalc := asmm.GetVPINCalculator("ETHUSDC")
	for i := 0; i < 10; i++ {
		for j := 0; j < 95; j++ {
			trade := strategy.Trade{
				Symbol:    "ETHUSDC",
				Price:     3000.1,
				Quantity:  100,
				Timestamp: time.Now(),
			}
			vpinCalc.UpdateMidPrice(3000.0)
			vpinCalc.UpdateTrade(trade)
		}

		for j := 0; j < 5; j++ {
			trade := strategy.Trade{
				Symbol:    "ETHUSDC",
				Price:     2999.9,
				Quantity:  100,
				Timestamp: time.Now(),
			}
			vpinCalc.UpdateTrade(trade)
		}
	}

	// 验证VPIN值
	stats := asmm.GetVPINStats("ETHUSDC")
	if stats.VPIN < 0.85 || stats.VPIN > 0.95 {
		t.Errorf("Expected VPIN around 0.9, got %.2f", stats.VPIN)
	}

	if !stats.ShouldPause {
		t.Error("Expected ShouldPause=true for high VPIN")
	}

	t.Logf("High VPIN value: %.4f (ShouldPause=%v)", stats.VPIN, stats.ShouldPause)

	// 生成报价，应该被暂停
	ctx := context.Background()
	_, _, err := asmm.GenerateQuotes(ctx, "ETHUSDC")

	if err == nil {
		t.Error("Expected error due to high VPIN, got nil")
	}

	if err != strategy.ErrHighVPINToxicity {
		t.Errorf("Expected ErrHighVPINToxicity, got %v", err)
	}

	t.Logf("Quotes paused due to high VPIN: %v", err)
}

// TestVPINIntegration_GrindingExemption 测试Grinding模式豁免VPIN暂停
func TestVPINIntegration_GrindingExemption(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			TotalNotionalMax: 10000,
		},
		Symbols: []config.SymbolConfig{
			{
				Symbol:                "ETHUSDC",
				NetMax:                1.0,
				MinSpread:             0.001,
				TickSize:              0.01,
				MinQty:                0.01,
				TotalLayers:           5,
				UnifiedLayerSize:      0.1,
				GridStartOffset:       1.0,
				GridFirstSpacing:      1.0,
				GridSpacingMultiplier: 1.15,
				GrindingEnabled:       true,
				GrindingThresh:        0.6, // 60%触发grinding
				VPINEnabled:           true,
				VPINBucketSize:        10000,
				VPINNumBuckets:        10,
				VPINThreshold:         0.7,
				VPINPauseThresh:       0.9,
				VPINMultiplier:        0.2,
				VPINVolThreshold:      10000,
			},
		},
	}

	st := store.NewStore("/tmp/test_vpin_grinding.json", 60*time.Second)
	defer st.Close()

	st.InitSymbol("ETHUSDC", 100)
	st.UpdateMidPrice("ETHUSDC", 3000.0, 2999.5, 3000.5)

	// 设置一个大仓位（70% NetMax）以触发grinding
	pos := store.Position{
		Symbol:     "ETHUSDC",
		Size:       0.7, // 70%的NetMax
		EntryPrice: 3000.0,
		Notional:   2100.0,
	}
	st.UpdatePosition("ETHUSDC", pos)

	asmm := strategy.NewASMM(cfg, st)

	vpinCfg := strategy.VPINConfig{
		BucketSize:   10000,
		NumBuckets:   10,
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 10000,
	}
	asmm.EnableVPIN("ETHUSDC", vpinCfg)

	// 注入高毒性流（VPIN > 0.9）
	vpinCalc := asmm.GetVPINCalculator("ETHUSDC")
	for i := 0; i < 10; i++ {
		for j := 0; j < 95; j++ {
			trade := strategy.Trade{
				Symbol:    "ETHUSDC",
				Price:     3000.1,
				Quantity:  100,
				Timestamp: time.Now(),
			}
			vpinCalc.UpdateMidPrice(3000.0)
			vpinCalc.UpdateTrade(trade)
		}

		for j := 0; j < 5; j++ {
			trade := strategy.Trade{
				Symbol:    "ETHUSDC",
				Price:     2999.9,
				Quantity:  100,
				Timestamp: time.Now(),
			}
			vpinCalc.UpdateTrade(trade)
		}
	}

	// 验证VPIN高且应该触发暂停
	stats := asmm.GetVPINStats("ETHUSDC")
	if stats.VPIN < 0.85 {
		t.Errorf("Expected high VPIN (>0.85), got %.2f", stats.VPIN)
	}

	// 验证应该触发grinding
	if !asmm.ShouldStartGrinding("ETHUSDC") {
		t.Error("Expected grinding mode to be triggered")
	}

	t.Logf("VPIN=%.4f (high), Position=70%% (grinding mode)", stats.VPIN)

	// 即使VPIN高，grinding模式也应该能生成报价（豁免暂停）
	ctx := context.Background()
	buyQuotes, sellQuotes, err := asmm.GenerateQuotes(ctx, "ETHUSDC")

	if err != nil {
		t.Fatalf("Grinding mode should be exempt from VPIN pause, but got error: %v", err)
	}

	if len(buyQuotes) == 0 && len(sellQuotes) == 0 {
		t.Error("Expected quotes in grinding mode despite high VPIN")
	}

	t.Logf("Grinding mode generated quotes despite high VPIN: %d buys, %d sells",
		len(buyQuotes), len(sellQuotes))
}

// TestVPINDisabledByDefault 测试VPIN默认禁用
func TestVPINDisabledByDefault(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			TotalNotionalMax: 10000,
		},
		Symbols: []config.SymbolConfig{
			{
				Symbol:                "ETHUSDC",
				NetMax:                1.0,
				MinSpread:             0.001,
				TickSize:              0.01,
				MinQty:                0.01,
				TotalLayers:           5,
				UnifiedLayerSize:      0.1,
				GridStartOffset:       1.0,
				GridFirstSpacing:      1.0,
				GridSpacingMultiplier: 1.15,
				VPINEnabled:           false, // 禁用VPIN
			},
		},
	}

	st := store.NewStore("/tmp/test_vpin_disabled.json", 60*time.Second)
	defer st.Close()

	st.InitSymbol("ETHUSDC", 100)
	st.UpdateMidPrice("ETHUSDC", 3000.0, 2999.5, 3000.5)

	asmm := strategy.NewASMM(cfg, st)

	// 不启用VPIN
	stats := asmm.GetVPINStats("ETHUSDC")
	if stats != nil {
		t.Error("VPIN stats should be nil when VPIN is disabled")
	}

	// 应该能正常生成报价
	ctx := context.Background()
	buyQuotes, sellQuotes, err := asmm.GenerateQuotes(ctx, "ETHUSDC")

	if err != nil {
		t.Fatalf("GenerateQuotes failed: %v", err)
	}

	if len(buyQuotes) == 0 || len(sellQuotes) == 0 {
		t.Error("Expected quotes even with VPIN disabled")
	}

	t.Logf("Generated quotes without VPIN: %d buys, %d sells",
		len(buyQuotes), len(sellQuotes))
}
