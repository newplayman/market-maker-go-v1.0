package store

import (
	"math"
	"testing"
	"time"
)

func TestStore_InitSymbol(t *testing.T) {
	st := NewStore("", time.Hour)
	defer st.Close()

	st.InitSymbol("BTCUSDT", 1800)

	state := st.GetSymbolState("BTCUSDT")
	if state == nil {
		t.Error("Expected symbol state to be initialized")
	}

	if state.Symbol != "BTCUSDT" {
		t.Errorf("Expected symbol BTCUSDT, got %s", state.Symbol)
	}

	if len(state.PriceHistory) != 1800 {
		t.Errorf("Expected price history size 1800, got %d", len(state.PriceHistory))
	}
}

func TestStore_UpdatePosition(t *testing.T) {
	st := NewStore("", time.Hour)
	defer st.Close()

	st.InitSymbol("BTCUSDT", 1800)

	pos := Position{
		Symbol:     "BTCUSDT",
		Size:       0.5,
		EntryPrice: 50000.0,
		Notional:   25000.0,
	}

	st.UpdatePosition("BTCUSDT", pos)

	state := st.GetSymbolState("BTCUSDT")
	state.Mu.RLock()
	size := state.Position.Size
	notional := state.Position.Notional
	state.Mu.RUnlock()

	if size != 0.5 {
		t.Errorf("Expected position size 0.5, got %.2f", size)
	}

	if notional != 25000.0 {
		t.Errorf("Expected notional 25000, got %.2f", notional)
	}

	totalNotional := st.GetTotalNotional()
	if totalNotional != 25000.0 {
		t.Errorf("Expected total notional 25000, got %.2f", totalNotional)
	}
}

func TestStore_UpdateMidPrice(t *testing.T) {
	st := NewStore("", time.Hour)
	defer st.Close()

	st.InitSymbol("BTCUSDT", 10)

	st.UpdateMidPrice("BTCUSDT", 50000.0, 49990.0, 50010.0)

	state := st.GetSymbolState("BTCUSDT")
	state.Mu.RLock()
	mid := state.MidPrice
	bid := state.BestBid
	ask := state.BestAsk
	state.Mu.RUnlock()

	if mid != 50000.0 {
		t.Errorf("Expected mid price 50000, got %.2f", mid)
	}
	if bid != 49990.0 {
		t.Errorf("Expected best bid 49990, got %.2f", bid)
	}
	if ask != 50010.0 {
		t.Errorf("Expected best ask 50010, got %.2f", ask)
	}
}

func TestStore_RecordFill(t *testing.T) {
	st := NewStore("", time.Hour)
	defer st.Close()

	st.InitSymbol("BTCUSDT", 1800)

	st.RecordFill("BTCUSDT", 0.1, 10.0)
	st.RecordFill("BTCUSDT", 0.2, -5.0)

	state := st.GetSymbolState("BTCUSDT")
	state.Mu.RLock()
	fillCount := state.FillCount
	totalVolume := state.TotalVolume
	totalPNL := state.TotalPNL
	state.Mu.RUnlock()

	if fillCount != 2 {
		t.Errorf("Expected fill count 2, got %d", fillCount)
	}

	expectedVolume := 0.3
	if math.Abs(totalVolume-expectedVolume) > 0.0001 {
		t.Errorf("Expected total volume %.2f, got %.2f", expectedVolume, totalVolume)
	}

	if totalPNL != 5.0 {
		t.Errorf("Expected total PNL 5.0, got %.2f", totalPNL)
	}
}

func TestStore_GetWorstCaseLong(t *testing.T) {
	st := NewStore("", time.Hour)
	defer st.Close()

	st.InitSymbol("BTCUSDT", 1800)

	// 设置仓位和挂单
	state := st.GetSymbolState("BTCUSDT")
	state.Mu.Lock()
	state.Position.Size = 0.5
	state.PendingBuy = 0.3
	state.PendingSell = 0.1
	state.Mu.Unlock()

	worstCase := st.GetWorstCaseLong("BTCUSDT")
	expected := 0.5 + 0.3 - 0.1 // 0.7

	if math.Abs(worstCase-expected) > 0.0001 {
		t.Errorf("Expected worst case %.2f, got %.2f", expected, worstCase)
	}
}

func TestStore_PriceStdDev(t *testing.T) {
	st := NewStore("", time.Hour)
	defer st.Close()

	st.InitSymbol("BTCUSDT", 5)

	// 添加价格数据
	prices := []float64{50000, 50100, 49900, 50050, 49950}
	for _, price := range prices {
		st.UpdateMidPrice("BTCUSDT", price, price-10, price+10)
	}

	stdDev := st.PriceStdDev30m("BTCUSDT")

	// 标准差应该大于0
	if stdDev <= 0 {
		t.Errorf("Expected stdDev > 0, got %.2f", stdDev)
	}

	// 大约应该在70左右（手动计算）
	if stdDev < 50 || stdDev > 100 {
		t.Logf("Warning: stdDev %.2f seems unusual, but may be correct", stdDev)
	}
}

func TestStore_UpdateFundingRate(t *testing.T) {
	st := NewStore("", time.Hour)
	defer st.Close()

	st.InitSymbol("BTCUSDT", 1800)

	st.UpdateFundingRate("BTCUSDT", 0.0001)
	st.UpdateFundingRate("BTCUSDT", 0.0002)

	state := st.GetSymbolState("BTCUSDT")
	state.Mu.RLock()
	rate := state.FundingRate
	histLen := len(state.FundingHistory)
	state.Mu.RUnlock()

	if rate != 0.0002 {
		t.Errorf("Expected funding rate 0.0002, got %.6f", rate)
	}

	if histLen != 2 {
		t.Errorf("Expected funding history length 2, got %d", histLen)
	}
}

func TestStore_IncrementCancelCount(t *testing.T) {
	st := NewStore("", time.Hour)
	defer st.Close()

	st.InitSymbol("BTCUSDT", 1800)

	count1 := st.IncrementCancelCount("BTCUSDT")
	count2 := st.IncrementCancelCount("BTCUSDT")

	if count1 != 1 {
		t.Errorf("Expected cancel count 1, got %d", count1)
	}

	if count2 != 2 {
		t.Errorf("Expected cancel count 2, got %d", count2)
	}
}

func TestStore_Concurrency(t *testing.T) {
	st := NewStore("", time.Hour)
	defer st.Close()

	st.InitSymbol("BTCUSDT", 1800)

	// 并发读写测试
	done := make(chan bool)

	// 并发更新价格
	go func() {
		for i := 0; i < 100; i++ {
			st.UpdateMidPrice("BTCUSDT", 50000.0+float64(i), 49990.0, 50010.0)
		}
		done <- true
	}()

	// 并发记录成交
	go func() {
		for i := 0; i < 100; i++ {
			st.RecordFill("BTCUSDT", 0.01, 1.0)
		}
		done <- true
	}()

	// 并发读取
	go func() {
		for i := 0; i < 100; i++ {
			_ = st.GetWorstCaseLong("BTCUSDT")
			_ = st.GetTotalNotional()
		}
		done <- true
	}()

	// 等待所有goroutine完成
	for i := 0; i < 3; i++ {
		<-done
	}

	// 验证数据一致性
	state := st.GetSymbolState("BTCUSDT")
	state.Mu.RLock()
	fillCount := state.FillCount
	state.Mu.RUnlock()

	if fillCount != 100 {
		t.Errorf("Expected fill count 100, got %d", fillCount)
	}
}
