package test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/newplayman/market-maker-phoenix/internal/config"
	gateway "github.com/newplayman/market-maker-phoenix/internal/exchange"
	"github.com/newplayman/market-maker-phoenix/internal/risk"
	"github.com/newplayman/market-maker-phoenix/internal/runner"
	"github.com/newplayman/market-maker-phoenix/internal/store"
	"github.com/newplayman/market-maker-phoenix/internal/strategy"
)

// MockExchange 实现Exchange接口，用于集成测试模拟
type MockExchange struct {
	mu                sync.Mutex
	placedOrders      []*gateway.Order
	canceledOrderIDs  []string
	openOrders        map[string][]*gateway.Order
	depthSubscribers  []func(*gateway.Depth)
	userStreamStarted bool
}

func NewMockExchange() *MockExchange {
	return &MockExchange{
		openOrders: make(map[string][]*gateway.Order),
	}
}

func (m *MockExchange) Connect(ctx context.Context) error {
	return nil
}

func (m *MockExchange) PlaceOrder(ctx context.Context, order *gateway.Order) (*gateway.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	order.ClientOrderID = "test_" + time.Now().Format("150405.000")
	order.Status = "NEW"
	m.placedOrders = append(m.placedOrders, order)
	m.openOrders[order.Symbol] = append(m.openOrders[order.Symbol], order)
	return order, nil
}

func (m *MockExchange) CancelOrder(ctx context.Context, symbol string, clientOrderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.canceledOrderIDs = append(m.canceledOrderIDs, clientOrderID)
	if orders, ok := m.openOrders[symbol]; ok {
		for i, o := range orders {
			if o.ClientOrderID == clientOrderID {
				m.openOrders[symbol] = append(orders[:i], orders[i+1:]...)
				break
			}
		}
	}
	return nil
}

func (m *MockExchange) CancelAllOrders(ctx context.Context, symbol string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.canceledOrderIDs = []string{}
	m.openOrders[symbol] = nil
	return nil
}

func (m *MockExchange) GetOpenOrders(ctx context.Context, symbol string) ([]*gateway.Order, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.openOrders[symbol], nil
}

func (m *MockExchange) GetPosition(ctx context.Context, symbol string) (*gateway.Position, error) {
	return &gateway.Position{Symbol: symbol}, nil
}

func (m *MockExchange) GetAllPositions(ctx context.Context) ([]*gateway.Position, error) {
	return nil, nil
}

func (m *MockExchange) GetFundingRate(ctx context.Context, symbol string) (*gateway.FundingRate, error) {
	return &gateway.FundingRate{Symbol: symbol, Rate: 0}, nil
}

func (m *MockExchange) GetDepth(ctx context.Context, symbol string, limit int) (*gateway.Depth, error) {
	return nil, nil
}

func (m *MockExchange) StartDepthStream(ctx context.Context, symbols []string, callback func(*gateway.Depth)) error {
	m.depthSubscribers = append(m.depthSubscribers, callback)
	return nil
}

func (m *MockExchange) StartUserStream(ctx context.Context, callbacks *gateway.UserStreamCallbacks) error {
	m.userStreamStarted = true
	return nil
}

func (m *MockExchange) Disconnect() error {
	return nil
}

func (m *MockExchange) IsConnected() bool {
	return true
}

// TestIntegration_Run 测试从启动到订单生成和同步的流程
func TestIntegration_Run(t *testing.T) {
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
				NearLayers:      1,
				FarLayers:       1,
				BaseLayerSize:   0.01,
				MaxCancelPerMin: 100,
			},
		},
	}

	st := store.NewStore("", time.Minute)
	st.InitSymbol("BTCUSDT", 100)
	st.UpdateMidPrice("BTCUSDT", 50000, 49995, 50005)

	riskMgr := risk.NewRiskManager(cfg, st)
	strat := strategy.NewASMM(cfg, st)
	mockExch := NewMockExchange()

	runner := runner.NewRunner(cfg, st, strat, riskMgr, mockExch)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go func() {
		if err := runner.Start(ctx); err != nil {
			t.Errorf("Runner start failed: %v", err)
		}
	}()

	time.Sleep(400 * time.Millisecond)
	runner.Stop()

	mockExch.mu.Lock()
	placedCount := len(mockExch.placedOrders)
	mockExch.mu.Unlock()

	if placedCount == 0 {
		t.Error("Expected at least one order placed")
	}
}
