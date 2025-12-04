package risk

import (
	"testing"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	"github.com/newplayman/market-maker-phoenix/internal/store"
)

// TestCheckBatchPreTrade_LightPositionPrinciple 测试轻仓做市原则
func TestCheckBatchPreTrade_LightPositionPrinciple(t *testing.T) {
	// 创建配置
	cfg := &config.Config{
		Symbols: []config.SymbolConfig{
			{
				Symbol:    "ETHUSDC",
				NetMax:    0.15,
				MinQty:    0.01,
				MinSpread: 0.0003,
			},
		},
	}

	// 创建store
	st := store.NewStore("./test_snapshot.json", 60)
	st.InitSymbol("ETHUSDC", 3600)

	// 更新仓位为0
	pos := store.Position{
		Symbol: "ETHUSDC",
		Size:   0.0,
	}
	st.UpdatePosition("ETHUSDC", pos)

	// 更新市场价格
	st.UpdateMidPrice("ETHUSDC", 3000.0, 2999.0, 3001.0)

	// 创建风控管理器
	rm := NewRiskManager(cfg, st)

	// 测试场景1：空仓时，双边各挂0.072 ETH（0.006*12层=0.072）
	// 最坏情况：0.072 ETH < 0.225 ETH (150% NetMax)，应该通过
	buyQuotes := make([]Quote, 12)
	sellQuotes := make([]Quote, 12)
	for i := 0; i < 12; i++ {
		buyQuotes[i] = Quote{Price: 2990.0 - float64(i), Size: 0.006, Layer: i + 1}
		sellQuotes[i] = Quote{Price: 3010.0 + float64(i), Size: 0.006, Layer: i + 1}
	}

	err := rm.CheckBatchPreTrade("ETHUSDC", buyQuotes, sellQuotes)
	if err != nil {
		t.Errorf("空仓时12层挂单应该通过，但失败: %v", err)
	}

	// 测试场景2：空仓时，双边各挂0.24 ETH（原配置：0.01*24层=0.24）
	// 最坏情况：0.24 ETH > 0.225 ETH (150% NetMax)，应该失败
	buyQuotes24 := make([]Quote, 24)
	sellQuotes24 := make([]Quote, 24)
	for i := 0; i < 24; i++ {
		buyQuotes24[i] = Quote{Price: 2990.0 - float64(i), Size: 0.01, Layer: i + 1}
		sellQuotes24[i] = Quote{Price: 3010.0 + float64(i), Size: 0.01, Layer: i + 1}
	}

	err = rm.CheckBatchPreTrade("ETHUSDC", buyQuotes24, sellQuotes24)
	if err == nil {
		t.Errorf("空仓时24层大挂单应该失败，但通过了")
	}

	// 测试场景3：已有0.05 ETH多头仓位，再挂0.20 ETH买单
	// 最坏情况：0.05 + 0.20 = 0.25 ETH > 0.225 ETH，应该失败
	pos.Size = 0.05
	st.UpdatePosition("ETHUSDC", pos)

	buyQuotesLarge := []Quote{
		{Price: 2990.0, Size: 0.2, Layer: 1},
	}
	sellQuotesSmall := []Quote{
		{Price: 3010.0, Size: 0.01, Layer: 1},
	}

	err = rm.CheckBatchPreTrade("ETHUSDC", buyQuotesLarge, sellQuotesSmall)
	if err == nil {
		t.Errorf("多头仓位时再挂超量买单应该失败，但通过了")
	}

	// 验证较小的买单仍然允许
	buyQuotesSmall := []Quote{
		{Price: 2990.0, Size: 0.02, Layer: 1},
	}
	err = rm.CheckBatchPreTrade("ETHUSDC", buyQuotesSmall, sellQuotesSmall)
	if err != nil {
		t.Errorf("多头仓位时挂少量买单应该通过，但失败: %v", err)
	}

	// 测试场景4：已有0.05 ETH多头仓位，挂0.05 ETH卖单（减仓方向）
	// 最坏情况：0.05 - 0.05 = 0.00 ETH < 0.075 ETH，应该通过
	buyQuotesEmpty := []Quote{}
	sellQuotesReduce := []Quote{
		{Price: 3010.0, Size: 0.05, Layer: 1},
	}

	err = rm.CheckBatchPreTrade("ETHUSDC", buyQuotesEmpty, sellQuotesReduce)
	if err != nil {
		t.Errorf("多头仓位时挂卖单（减仓）应该通过，但失败: %v", err)
	}

	// 测试场景5：已有0.15 ETH多头仓位，已经触发Grinding
	pos.Size = 0.15
	st.UpdatePosition("ETHUSDC", pos)

	buyQuotesTooBig := []Quote{
		{Price: 2990.0, Size: 0.1, Layer: 1},
	}
	sellQuotesTiny := []Quote{
		{Price: 3010.0, Size: 0.01, Layer: 1},
	}

	err = rm.CheckBatchPreTrade("ETHUSDC", buyQuotesTooBig, sellQuotesTiny)
	if err == nil {
		t.Errorf("高仓位时再挂买单应该失败，但通过了")
	}
}

// TestCheckBatchPreTrade_AsymmetricPositions 测试非对称仓位下的风控
func TestCheckBatchPreTrade_AsymmetricPositions(t *testing.T) {
	cfg := &config.Config{
		Symbols: []config.SymbolConfig{
			{
				Symbol:    "ETHUSDC",
				NetMax:    0.15,
				MinQty:    0.01,
				MinSpread: 0.0003,
			},
		},
	}

	st := store.NewStore("./test_snapshot.json", 60)
	st.InitSymbol("ETHUSDC", 3600)

	// 多头仓位0.06 ETH（40% NetMax）
	pos := store.Position{
		Symbol: "ETHUSDC",
		Size:   0.06,
	}
	st.UpdatePosition("ETHUSDC", pos)
	st.UpdateMidPrice("ETHUSDC", 3000.0, 2999.0, 3001.0)

	rm := NewRiskManager(cfg, st)

	// 减仓方向（卖单）应该更宽松
	// 允许挂更多卖单：0.06 (当前) + 0.075 (50% NetMax) = 0.135 ETH卖单容量
	// 但买单容量受限：0.075 - 0.06 = 0.015 ETH

	// 测试：挂0.20 ETH买单应该失败
	buyQuotesOver := []Quote{
		{Price: 2990.0, Size: 0.2, Layer: 1},
	}
	sellQuotesNormal := []Quote{
		{Price: 3010.0, Size: 0.02, Layer: 1},
	}

	err := rm.CheckBatchPreTrade("ETHUSDC", buyQuotesOver, sellQuotesNormal)
	if err == nil {
		t.Errorf("多头仓位时挂过多买单应该失败，但通过了")
	}

	// 测试：挂0.01 ETH买单和0.10 ETH卖单应该通过
	buyQuotesOK := []Quote{
		{Price: 2990.0, Size: 0.01, Layer: 1},
	}
	sellQuotesMany := []Quote{
		{Price: 3010.0, Size: 0.05, Layer: 1},
		{Price: 3015.0, Size: 0.05, Layer: 2},
	}

	err = rm.CheckBatchPreTrade("ETHUSDC", buyQuotesOK, sellQuotesMany)
	if err != nil {
		t.Errorf("多头仓位时挂少量买单和较多卖单应该通过，但失败: %v", err)
	}
}

// TestCheckBatchPreTrade_EdgeCases 测试边界情况
func TestCheckBatchPreTrade_EdgeCases(t *testing.T) {
	cfg := &config.Config{
		Symbols: []config.SymbolConfig{
			{
				Symbol:    "ETHUSDC",
				NetMax:    0.15,
				MinQty:    0.01,
				MinSpread: 0.0003,
			},
		},
	}

	st := store.NewStore("./test_snapshot.json", 60)
	st.InitSymbol("ETHUSDC", 3600)

	pos := store.Position{
		Symbol: "ETHUSDC",
		Size:   0.0,
	}
	st.UpdatePosition("ETHUSDC", pos)
	st.UpdateMidPrice("ETHUSDC", 3000.0, 2999.0, 3001.0)

	rm := NewRiskManager(cfg, st)

	// 测试：空报价列表应该通过
	err := rm.CheckBatchPreTrade("ETHUSDC", []Quote{}, []Quote{})
	if err != nil {
		t.Errorf("空报价列表应该通过，但失败: %v", err)
	}

	// 测试：刚好等于150% NetMax应该通过（考虑浮点误差留出极小余量）
	exactSize := cfg.Symbols[0].NetMax*1.5 - 1e-6
	buyQuotesExact := []Quote{
		{Price: 2990.0, Size: exactSize, Layer: 1},
	}

	err = rm.CheckBatchPreTrade("ETHUSDC", buyQuotesExact, []Quote{})
	if err != nil {
		t.Errorf("刚好等于50%% NetMax应该通过，但失败: %v", err)
	}

	// 测试：超过150% NetMax一点应该失败
	overSize := cfg.Symbols[0].NetMax*1.5 + 0.01
	buyQuotesOver := []Quote{
		{Price: 2990.0, Size: overSize, Layer: 1},
	}

	err = rm.CheckBatchPreTrade("ETHUSDC", buyQuotesOver, []Quote{})
	if err == nil {
		t.Errorf("超过50%% NetMax应该失败，但通过了")
	}
}
