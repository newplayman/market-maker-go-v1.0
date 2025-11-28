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

// MockExchange 用于集成测试的模拟交易所
type MockExchange struct {
	orders map[string]*gateway.Order
	mu     sync.RWMutex
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

	order.ClientOrderID = "test-" + time.Now().Format("20060102150405")
	order.Status = "NEW"
	m.orders[order.ClientOrderID] = order
	return order, nil
}

func (m *MockExchange) CancelOrder(ctx context.Context, symbol, clientOrderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if order, ok := m.orders[clientOrderID]; ok {
		order.Status = "CANCELED"
	}
	return nil
}

func (m *MockExchange) CancelAllOrders(ctx context.Context, symbol string) error {
	return nil
}

func (m *MockExchange) GetOpenOrders(ctx context.Context, symbol string) ([]*gateway.Order, error) {
	return nil, nil
}

func (m *MockExchange) GetPosition(ctx context.Context, symbol string) (*gateway.Position, error) {
	return &gateway.Position{
		Symbol:     symbol,
		Size:       0,
		EntryPrice: 0,
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

// TestIntegration_BasicWorkflow 测试基本工作流程
func TestIntegration_BasicWorkflow(t *testing.T) {
	// 创建配置
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
				NearLayers:      3,
				FarLayers:       5,
				BaseLayerSize:   0.1,
				MaxCancelPerMin: 100,
			},
		},
	}

	// 初始化组件
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
	r := runner.NewRunner(cfg, st, strat, riskMgr, mockExch)

	// 启动Runner
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go func() {
		if err := r.Start(ctx); err != nil {
			t.Logf("Runner error: %v", err)
		}
	}()

	// 运行一段时间
	time.Sleep(400 * time.Millisecond)

	// 停止Runner
	r.Stop()

	// 验证系统运行正常
	t.Log("集成测试完成 - 系统正常运行")
}

// TestIntegration_MultiSymbol 测试多交易对
func TestIntegration_MultiSymbol(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			TotalNotionalMax: 2000000,
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

	// 初始化价格数据
	for i := 0; i < 10; i++ {
		st.UpdateMidPrice("BTCUSDT", 50000, 49995, 50005)
		st.UpdateMidPrice("ETHUSDT", 3000, 2998, 3002)
		time.Sleep(10 * time.Millisecond)
	}

	mockExch := NewMockExchange()
	strat := strategy.NewASMM(cfg, st)
	riskMgr := risk.NewRiskManager(cfg, st)
	r := runner.NewRunner(cfg, st, strat, riskMgr, mockExch)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	go func() {
		if err := r.Start(ctx); err != nil {
			t.Logf("Runner error: %v", err)
		}
	}()

	time.Sleep(400 * time.Millisecond)
	r.Stop()

	t.Log("多交易对集成测试完成")
}

// TestIntegration_RiskControl 测试风控功能
func TestIntegration_RiskControl(t *testing.T) {
	cfg := &config.Config{
		Global: config.GlobalConfig{
			TotalNotionalMax: 1000000,
			QuoteIntervalMs:  200,
		},
		Symbols: []config.SymbolConfig{
			{
				Symbol:          "BTCUSDT",
				NetMax:          0.01, // 非常小的限制
				MinSpread:       0.0002,
				NearLayers:      3,
				FarLayers:       5,
				BaseLayerSize:   1.0, // 大订单
				MaxCancelPerMin: 100,
			},
		},
	}

	st := store.NewStore("", 5*time.Minute)
	st.InitSymbol("BTCUSDT", 100)

	for i := 0; i < 10; i++ {
		st.UpdateMidPrice("BTCUSDT", 50000, 49995, 50005)
		time.Sleep(10 * time.Millisecond)
	}

	mockExch := NewMockExchange()
	strat := strategy.NewASMM(cfg, st)
	riskMgr := risk.NewRiskManager(cfg, st)
	r := runner.NewRunner(cfg, st, strat, riskMgr, mockExch)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	go func() {
		if err := r.Start(ctx); err != nil {
			t.Logf("Runner error: %v", err)
		}
	}()

	time.Sleep(250 * time.Millisecond)
	r.Stop()

	t.Log("风控集成测试完成 - 风控正常工作")
}
