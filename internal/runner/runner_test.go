package runner

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	gateway "github.com/newplayman/market-maker-phoenix/internal/exchange"
	"github.com/newplayman/market-maker-phoenix/internal/risk"
	"github.com/newplayman/market-maker-phoenix/internal/store"
	"github.com/newplayman/market-maker-phoenix/internal/strategy"
)

// MockExchange 用于测试的模拟交易所
type MockExchange struct {
	mu                sync.Mutex
	placeOrderCalled  int
	cancelOrderCalled int
	orders            map[string]*gateway.Order
}

func NewMockExchange() *MockExchange {
	return &MockExchange{
		orders: make(map[string]*gateway.Order),
	}
}

func (m *MockExchange) Connect(ctx context.Context) error {
	return nil
}

func (m *MockExchange) PlaceOrder(ctx context.Context, order *gateway.Order) (*gateway.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.placeOrderCalled++
	order.ClientOrderID = "test-order-" + time.Now().Format("20060102150405")
	order.Status = "NEW"
	m.orders[order.ClientOrderID] = order
	return order, nil
}

func (m *MockExchange) CancelOrder(ctx context.Context, symbol, clientOrderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cancelOrderCalled++
	if order, ok := m.orders[clientOrderID]; ok {
		order.Status = "CANCELED"
	}
	return nil
}

func (m *MockExchange) CancelAllOrders(ctx context.Context, symbol string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cancelOrderCalled++
	return nil
}

func (m *MockExchange) GetOpenOrders(ctx context.Context, symbol string) ([]*gateway.Order, error) {
	return nil, nil
}

func (m *MockExchange) GetPosition(ctx context.Context, symbol string) (*gateway.Position, error) {
	return &gateway.Position{
		Symbol:           symbol,
		Size:             0,
		EntryPrice:       0,
		Notional:         0,
		Leverage:         1,
		LiquidationPrice: 0,
	}, nil
}

func (m *MockExchange) GetAllPositions(ctx context.Context) ([]*gateway.Position, error) {
	return nil, nil
}

func (m *MockExchange) GetFundingRate(ctx context.Context, symbol string) (*gateway.FundingRate, error) {
	return &gateway.FundingRate{Symbol: symbol, Rate: 0.0001}, nil
}

func (m *MockExchange) GetDepth(ctx context.Context, symbol string, limit int) (*gateway.Depth, error) {
	return nil, nil
}

func (m *MockExchange) StartDepthStream(ctx context.Context, symbols []string, callback func(*gateway.Depth)) error {
	return nil
}

func (m *MockExchange) StartUserStream(ctx context.Context, callbacks *gateway.UserStreamCallbacks) error {
	return nil
}

func (m *MockExchange) Disconnect() error {
	return nil
}

func (m *MockExchange) IsConnected() bool {
	return true
}

func TestRunner_Initialization(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			TotalNotionalMax: 1000000,
			QuoteIntervalMs:  500,
		},
		Symbols: []config.SymbolConfig{
			{
				Symbol:        "BTCUSDT",
				NetMax:        1.0,
				MinSpread:     0.0002,
				NearLayers:    3,
				FarLayers:     5,
				BaseLayerSize: 0.1,
			},
		},
	}

	st := store.NewStore("", 5*time.Minute)
	st.InitSymbol("BTCUSDT", 100) // 价格历史大小

	mockExch := NewMockExchange()
	strat := strategy.NewASMM(cfg, st)
	riskMgr := risk.NewRiskManager(cfg, st)

	runner := NewRunner(cfg, st, strat, riskMgr, mockExch)

	if runner == nil {
		t.Fatal("Expected runner to be initialized")
	}

	if runner.cfg != cfg {
		t.Error("Config not set correctly")
	}

	if runner.store != st {
		t.Error("Store not set correctly")
	}
}

func TestRunner_StartStop(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			TotalNotionalMax: 1000000,
			QuoteIntervalMs:  100,
		},
		Symbols: []config.SymbolConfig{
			{
				Symbol:          "BTCUSDT",
				NetMax:          1.0,
				MinSpread:       0.0002,
				NearLayers:      3,
				FarLayers:       5,
				BaseLayerSize:   0.1,
				MaxCancelPerMin: 100, // 提高限制避免flicker
			},
		},
	}

	st := store.NewStore("", 5*time.Minute)
	st.InitSymbol("BTCUSDT", 100)
	// 初始化价格数据
	for i := 0; i < 10; i++ {
		st.UpdateMidPrice("BTCUSDT", 50000, 49995, 50005)
		time.Sleep(10 * time.Millisecond)
	}

	mockExch := NewMockExchange()
	strat := strategy.NewASMM(cfg, st)
	riskMgr := risk.NewRiskManager(cfg, st)

	runner := NewRunner(cfg, st, strat, riskMgr, mockExch)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// 启动runner
	go func() {
		if err := runner.Start(ctx); err != nil {
			t.Logf("Runner start error: %v", err)
		}
	}()

	// 等待一段时间让runner运行
	time.Sleep(300 * time.Millisecond)

	// 停止runner
	runner.Stop()

	// 验证订单被下达
	if mockExch.placeOrderCalled == 0 {
		t.Error("Expected PlaceOrder to be called")
	}
}

func TestRunner_QuoteGeneration(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			TotalNotionalMax: 1000000,
			QuoteIntervalMs:  200,
		},
		Symbols: []config.SymbolConfig{
			{
				Symbol:          "BTCUSDT",
				NetMax:          1.0,
				MinSpread:       0.0002,
				NearLayers:      2,
				FarLayers:       3,
				BaseLayerSize:   0.1,
				MaxCancelPerMin: 100, // 提高限制避免flicker
			},
		},
	}

	st := store.NewStore("", 5*time.Minute)
	st.InitSymbol("BTCUSDT", 100)
	// 初始化价格历史
	for i := 0; i < 10; i++ {
		st.UpdateMidPrice("BTCUSDT", 50000, 49995, 50005)
		time.Sleep(10 * time.Millisecond)
	}

	mockExch := NewMockExchange()
	strat := strategy.NewASMM(cfg, st)
	riskMgr := risk.NewRiskManager(cfg, st)

	runner := NewRunner(cfg, st, strat, riskMgr, mockExch)

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	go func() {
		if err := runner.Start(ctx); err != nil {
			t.Logf("Runner start error: %v", err)
		}
	}()

	time.Sleep(350 * time.Millisecond)
	runner.Stop()

	// 验证生成了报价
	if mockExch.placeOrderCalled < 2 {
		t.Errorf("Expected at least 2 PlaceOrder calls, got %d", mockExch.placeOrderCalled)
	}
}

func TestRunner_ErrorHandling(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			TotalNotionalMax: 1000000,
			QuoteIntervalMs:  100,
		},
		Symbols: []config.SymbolConfig{
			{
				Symbol:          "BTCUSDT",
				NetMax:          0.01, // 非常小的限制，容易触发风控
				MinSpread:       0.0002,
				NearLayers:      3,
				FarLayers:       5,
				BaseLayerSize:   1.0, // 大订单，会被风控拒绝
				MaxCancelPerMin: 100,
			},
		},
	}

	st := store.NewStore("", 5*time.Minute)
	st.InitSymbol("BTCUSDT", 100)
	// 初始化价格历史
	for i := 0; i < 10; i++ {
		st.UpdateMidPrice("BTCUSDT", 50000, 49995, 50005)
		time.Sleep(10 * time.Millisecond)
	}

	mockExch := NewMockExchange()
	strat := strategy.NewASMM(cfg, st)
	riskMgr := risk.NewRiskManager(cfg, st)

	runner := NewRunner(cfg, st, strat, riskMgr, mockExch)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	go func() {
		if err := runner.Start(ctx); err != nil {
			t.Logf("Runner start error: %v", err)
		}
	}()

	time.Sleep(250 * time.Millisecond)
	runner.Stop()

	// Runner应该能够处理风控错误而不崩溃
	// 这个测试主要验证错误处理不会导致panic
}

func TestRunner_MultiSymbol(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			TotalNotionalMax: 1000000,
			QuoteIntervalMs:  200,
		},
		Symbols: []config.SymbolConfig{
			{
				Symbol:          "BTCUSDT",
				NetMax:          1.0,
				MinSpread:       0.0002,
				NearLayers:      2,
				FarLayers:       3,
				BaseLayerSize:   0.1,
				MaxCancelPerMin: 100,
			},
			{
				Symbol:          "ETHUSDT",
				NetMax:          2.0,
				MinSpread:       0.0002,
				NearLayers:      2,
				FarLayers:       3,
				BaseLayerSize:   0.2,
				MaxCancelPerMin: 100,
			},
		},
	}

	st := store.NewStore("", 5*time.Minute)
	st.InitSymbol("BTCUSDT", 100)
	st.InitSymbol("ETHUSDT", 100)
	// 初始化价格历史
	for i := 0; i < 10; i++ {
		st.UpdateMidPrice("BTCUSDT", 50000, 49995, 50005)
		st.UpdateMidPrice("ETHUSDT", 3000, 2998, 3002)
		time.Sleep(10 * time.Millisecond)
	}

	mockExch := NewMockExchange()
	strat := strategy.NewASMM(cfg, st)
	riskMgr := risk.NewRiskManager(cfg, st)

	runner := NewRunner(cfg, st, strat, riskMgr, mockExch)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go func() {
		if err := runner.Start(ctx); err != nil {
			t.Logf("Runner start error: %v", err)
		}
	}()

	time.Sleep(450 * time.Millisecond)
	runner.Stop()

	// 验证两个交易对都有订单
	if mockExch.placeOrderCalled < 4 {
		t.Errorf("Expected at least 4 PlaceOrder calls for 2 symbols, got %d", mockExch.placeOrderCalled)
	}
}
