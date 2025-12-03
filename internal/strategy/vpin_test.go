package strategy

import (
	"math"
	"sync"
	"testing"
	"time"
)

func TestNewVPINCalculator(t *testing.T) {
	cfg := VPINConfig{
		BucketSize:   10000,
		NumBuckets:   50,
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 100000,
	}

	calc := NewVPINCalculator("ETHUSDC", cfg)

	if calc == nil {
		t.Fatal("NewVPINCalculator returned nil")
	}

	if calc.symbol != "ETHUSDC" {
		t.Errorf("Expected symbol ETHUSDC, got %s", calc.symbol)
	}

	if len(calc.buckets) != 50 {
		t.Errorf("Expected 50 buckets, got %d", len(calc.buckets))
	}
}

func TestVPINCalculation_HighBuyPressure(t *testing.T) {
	cfg := VPINConfig{
		BucketSize:   10000, // 小bucket以便快速测试
		NumBuckets:   10,
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 10000, // 降低阈值以便测试
	}

	calc := NewVPINCalculator("ETHUSDC", cfg)
	calc.UpdateMidPrice(3000.0)

	// 模拟80%买盘，20%卖盘的毒性流
	// 每个bucket 10000，填充10个buckets
	for i := 0; i < 10; i++ {
		// 80%买盘 = 8000
		for j := 0; j < 8; j++ {
			trade := Trade{
				Symbol:    "ETHUSDC",
				Price:     3000.1, // 高于mid，标记为买盘
				Quantity:  1000,
				Timestamp: time.Now(),
			}
			err := calc.UpdateTrade(trade)
			if err != nil {
				t.Fatalf("UpdateTrade failed: %v", err)
			}
		}

		// 20%卖盘 = 2000
		for j := 0; j < 2; j++ {
			trade := Trade{
				Symbol:    "ETHUSDC",
				Price:     2999.9, // 低于mid，标记为卖盘
				Quantity:  1000,
				Timestamp: time.Now(),
			}
			err := calc.UpdateTrade(trade)
			if err != nil {
				t.Fatalf("UpdateTrade failed: %v", err)
			}
		}
	}

	// 计算VPIN
	vpin := calc.GetVPIN()

	// 期望: |8000-2000| / 10000 = 6000/10000 = 0.6
	expected := 0.6
	tolerance := 0.01

	if math.Abs(vpin-expected) > tolerance {
		t.Errorf("Expected VPIN ~%.2f, got %.2f", expected, vpin)
	}

	stats := calc.GetStats()
	if stats.IsWarning {
		t.Error("Expected IsWarning=false for VPIN=0.6 with threshold=0.7")
	}

	t.Logf("VPIN: %.4f, Filled Buckets: %d, Total Trades: %d",
		stats.VPIN, stats.FilledBuckets, stats.TotalTrades)
}

func TestVPINCalculation_BalancedFlow(t *testing.T) {
	cfg := VPINConfig{
		BucketSize:   10000,
		NumBuckets:   10,
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 10000,
	}

	calc := NewVPINCalculator("ETHUSDC", cfg)
	calc.UpdateMidPrice(3000.0)

	// 模拟50%买盘，50%卖盘的平衡流
	for i := 0; i < 10; i++ {
		// 50%买盘 = 5000
		for j := 0; j < 5; j++ {
			trade := Trade{
				Symbol:    "ETHUSDC",
				Price:     3000.1,
				Quantity:  1000,
				Timestamp: time.Now(),
			}
			calc.UpdateTrade(trade)
		}

		// 50%卖盘 = 5000
		for j := 0; j < 5; j++ {
			trade := Trade{
				Symbol:    "ETHUSDC",
				Price:     2999.9,
				Quantity:  1000,
				Timestamp: time.Now(),
			}
			calc.UpdateTrade(trade)
		}
	}

	vpin := calc.GetVPIN()

	// 期望: |5000-5000| / 10000 = 0.0 (完全平衡)
	expected := 0.0
	tolerance := 0.05

	if math.Abs(vpin-expected) > tolerance {
		t.Errorf("Expected VPIN ~%.2f, got %.2f", expected, vpin)
	}

	stats := calc.GetStats()
	if stats.IsWarning {
		t.Error("Expected IsWarning=false for balanced flow")
	}
}

func TestVPINCalculation_ExtremelyHighToxicity(t *testing.T) {
	cfg := VPINConfig{
		BucketSize:   10000,
		NumBuckets:   10,
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 10000,
	}

	calc := NewVPINCalculator("ETHUSDC", cfg)
	calc.UpdateMidPrice(3000.0)

	// 模拟95%买盘，5%卖盘的极端毒性流
	for i := 0; i < 10; i++ {
		// 95%买盘 = 9500
		for j := 0; j < 95; j++ {
			trade := Trade{
				Symbol:    "ETHUSDC",
				Price:     3000.1,
				Quantity:  100,
				Timestamp: time.Now(),
			}
			calc.UpdateTrade(trade)
		}

		// 5%卖盘 = 500
		for j := 0; j < 5; j++ {
			trade := Trade{
				Symbol:    "ETHUSDC",
				Price:     2999.9,
				Quantity:  100,
				Timestamp: time.Now(),
			}
			calc.UpdateTrade(trade)
		}
	}

	vpin := calc.GetVPIN()

	// 期望: |9500-500| / 10000 = 9000/10000 = 0.9
	expected := 0.9
	tolerance := 0.02

	if math.Abs(vpin-expected) > tolerance {
		t.Errorf("Expected VPIN ~%.2f, got %.2f", expected, vpin)
	}

	stats := calc.GetStats()
	if !stats.ShouldPause {
		t.Error("Expected ShouldPause=true for VPIN=0.9")
	}
}

func TestVPINFallback_InsufficientData(t *testing.T) {
	cfg := VPINConfig{
		BucketSize:   10000,
		NumBuckets:   50,
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 100000,
	}

	calc := NewVPINCalculator("ETHUSDC", cfg)
	calc.UpdateMidPrice(3000.0)

	// 只添加少量数据，不足5个buckets
	for i := 0; i < 3; i++ {
		for j := 0; j < 100; j++ {
			trade := Trade{
				Symbol:    "ETHUSDC",
				Price:     3000.1,
				Quantity:  100,
				Timestamp: time.Now(),
			}
			calc.UpdateTrade(trade)
		}
	}

	vpin := calc.GetVPIN()

	// 数据不足时应返回中性值0.5
	expected := 0.5
	if vpin != expected {
		t.Errorf("Expected VPIN=%.1f (fallback) with insufficient data, got %.2f", expected, vpin)
	}
}

func TestVPINFallback_LowVolume(t *testing.T) {
	cfg := VPINConfig{
		BucketSize:   100, // 小bucket
		NumBuckets:   10,
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 100000, // 高阈值
	}

	calc := NewVPINCalculator("ETHUSDC", cfg)
	calc.UpdateMidPrice(3000.0)

	// 填充10个buckets，但总量不足VolThreshold
	for i := 0; i < 10; i++ {
		for j := 0; j < 10; j++ {
			trade := Trade{
				Symbol:    "ETHUSDC",
				Price:     3000.1,
				Quantity:  10,
				Timestamp: time.Now(),
			}
			calc.UpdateTrade(trade)
		}
	}

	vpin := calc.GetVPIN()

	// 总成交量不足时应返回中性值0.5
	expected := 0.5
	if vpin != expected {
		t.Errorf("Expected VPIN=%.1f (fallback) with low volume, got %.2f", expected, vpin)
	}
}

func TestVPINBucketRolling(t *testing.T) {
	cfg := VPINConfig{
		BucketSize:   1000,
		NumBuckets:   5, // 只保留5个buckets
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 1000,
	}

	calc := NewVPINCalculator("ETHUSDC", cfg)
	calc.UpdateMidPrice(3000.0)

	// 填充超过NumBuckets数量的buckets，测试环形缓冲
	for i := 0; i < 10; i++ {
		// 填充一个bucket
		for j := 0; j < 100; j++ {
			trade := Trade{
				Symbol:    "ETHUSDC",
				Price:     3000.1,
				Quantity:  10,
				Timestamp: time.Now(),
			}
			calc.UpdateTrade(trade)
		}
	}

	stats := calc.GetStats()

	// 应该只保留5个buckets
	if stats.FilledBuckets > cfg.NumBuckets {
		t.Errorf("Expected max %d filled buckets, got %d", cfg.NumBuckets, stats.FilledBuckets)
	}

	// 总交易数应该是1000（10个bucket × 100笔交易）
	if stats.TotalTrades != 1000 {
		t.Errorf("Expected 1000 total trades, got %d", stats.TotalTrades)
	}
}

func TestVPINConfigUpdate(t *testing.T) {
	cfg := VPINConfig{
		BucketSize:   10000,
		NumBuckets:   10,
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 10000,
	}

	calc := NewVPINCalculator("ETHUSDC", cfg)

	// 更新配置
	newCfg := VPINConfig{
		Threshold:   0.8,
		PauseThresh: 0.95,
		Multiplier:  0.3,
	}

	calc.UpdateConfig(newCfg)

	updatedCfg := calc.GetConfig()

	if updatedCfg.Threshold != 0.8 {
		t.Errorf("Expected Threshold=0.8, got %.2f", updatedCfg.Threshold)
	}

	if updatedCfg.PauseThresh != 0.95 {
		t.Errorf("Expected PauseThresh=0.95, got %.2f", updatedCfg.PauseThresh)
	}

	if updatedCfg.Multiplier != 0.3 {
		t.Errorf("Expected Multiplier=0.3, got %.2f", updatedCfg.Multiplier)
	}
}

func TestVPINReset(t *testing.T) {
	cfg := VPINConfig{
		BucketSize:   10000,
		NumBuckets:   10,
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 10000,
	}

	calc := NewVPINCalculator("ETHUSDC", cfg)
	calc.UpdateMidPrice(3000.0)

	// 添加一些数据
	for i := 0; i < 100; i++ {
		trade := Trade{
			Symbol:    "ETHUSDC",
			Price:     3000.1,
			Quantity:  100,
			Timestamp: time.Now(),
		}
		calc.UpdateTrade(trade)
	}

	stats := calc.GetStats()
	if stats.TotalTrades == 0 {
		t.Error("Expected trades before reset")
	}

	// 重置
	calc.Reset()

	stats = calc.GetStats()
	if stats.TotalTrades != 0 {
		t.Errorf("Expected 0 trades after reset, got %d", stats.TotalTrades)
	}

	if stats.FilledBuckets != 0 {
		t.Errorf("Expected 0 filled buckets after reset, got %d", stats.FilledBuckets)
	}
}

func TestVPINConcurrency(t *testing.T) {
	cfg := VPINConfig{
		BucketSize:   10000,
		NumBuckets:   50,
		Threshold:    0.7,
		PauseThresh:  0.9,
		Multiplier:   0.2,
		VolThreshold: 10000,
	}

	calc := NewVPINCalculator("ETHUSDC", cfg)
	calc.UpdateMidPrice(3000.0)

	// 并发写入和读取测试
	var wg sync.WaitGroup
	numWriters := 10
	numReaders := 5
	tradesPerWriter := 100

	// 启动写goroutines
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < tradesPerWriter; j++ {
				trade := Trade{
					Symbol:    "ETHUSDC",
					Price:     3000.0 + float64(id%2)*0.1, // 交替买卖
					Quantity:  100,
					Timestamp: time.Now(),
				}
				calc.UpdateTrade(trade)
			}
		}(i)
	}

	// 启动读goroutines
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < tradesPerWriter*2; j++ {
				_ = calc.GetVPIN()
				_ = calc.GetStats()
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()

	stats := calc.GetStats()
	expectedTrades := int64(numWriters * tradesPerWriter)

	if stats.TotalTrades != expectedTrades {
		t.Errorf("Expected %d total trades, got %d", expectedTrades, stats.TotalTrades)
	}

	t.Logf("Concurrency test passed: %d trades, %d buckets, VPIN=%.4f",
		stats.TotalTrades, stats.FilledBuckets, stats.VPIN)
}

func TestVPINInvalidTrade(t *testing.T) {
	cfg := DefaultVPINConfig()
	calc := NewVPINCalculator("ETHUSDC", cfg)

	// 测试无效数量
	trade := Trade{
		Symbol:    "ETHUSDC",
		Price:     3000.0,
		Quantity:  -100, // 负数量
		Timestamp: time.Now(),
	}

	err := calc.UpdateTrade(trade)
	if err != ErrInvalidTradeQuantity {
		t.Errorf("Expected ErrInvalidTradeQuantity, got %v", err)
	}

	// 测试零数量
	trade.Quantity = 0
	err = calc.UpdateTrade(trade)
	if err != ErrInvalidTradeQuantity {
		t.Errorf("Expected ErrInvalidTradeQuantity for zero quantity, got %v", err)
	}
}

func TestVPINEdgeCases(t *testing.T) {
	// 测试默认配置
	cfg := DefaultVPINConfig()
	if cfg.BucketSize != 50000 {
		t.Errorf("Expected default BucketSize=50000, got %.0f", cfg.BucketSize)
	}

	// 测试无效配置自动修正
	badCfg := VPINConfig{
		BucketSize: 0,
		NumBuckets: 0,
	}

	calc := NewVPINCalculator("ETHUSDC", badCfg)
	if calc.config.BucketSize <= 0 {
		t.Error("Expected BucketSize to be corrected to positive value")
	}
	if calc.config.NumBuckets <= 0 {
		t.Error("Expected NumBuckets to be corrected to positive value")
	}
}

func BenchmarkVPINUpdate(b *testing.B) {
	cfg := DefaultVPINConfig()
	calc := NewVPINCalculator("ETHUSDC", cfg)
	calc.UpdateMidPrice(3000.0)

	trade := Trade{
		Symbol:    "ETHUSDC",
		Price:     3000.1,
		Quantity:  100,
		Timestamp: time.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		calc.UpdateTrade(trade)
	}
}

func BenchmarkVPINGetVPIN(b *testing.B) {
	cfg := DefaultVPINConfig()
	calc := NewVPINCalculator("ETHUSDC", cfg)
	calc.UpdateMidPrice(3000.0)

	// 预先填充一些数据
	for i := 0; i < 1000; i++ {
		trade := Trade{
			Symbol:    "ETHUSDC",
			Price:     3000.1,
			Quantity:  100,
			Timestamp: time.Now(),
		}
		calc.UpdateTrade(trade)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		calc.GetVPIN()
	}
}

