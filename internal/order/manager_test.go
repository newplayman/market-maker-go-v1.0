package order

import (
	"context"
	"errors"
	"testing"
	"time"

	gateway "github.com/newplayman/market-maker-phoenix/internal/exchange"
	"github.com/newplayman/market-maker-phoenix/internal/store"
)

// mockExchange 实现exchange.Exchange接口，用于测试
type mockExchange struct {
	openOrders  map[string][]*gateway.Order
	cancelCalls []string
	placeCalls  []*gateway.Order
	failCancel  bool
	failPlace   bool
}

func (m *mockExchange) GetOpenOrders(ctx context.Context, symbol string) ([]*gateway.Order, error) {
	if orders, ok := m.openOrders[symbol]; ok {
		return orders, nil
	}
	return nil, nil
}

func (m *mockExchange) CancelOrder(ctx context.Context, symbol string, clientOrderID string) error {
	m.cancelCalls = append(m.cancelCalls, clientOrderID)
	if m.failCancel {
		return errors.New("cancel error")
	}
	return nil
}

func (m *mockExchange) PlaceOrder(ctx context.Context, order *gateway.Order) (*gateway.Order, error) {
	m.placeCalls = append(m.placeCalls, order)
	if m.failPlace {
		return nil, errors.New("place error")
	}
	order.ClientOrderID = "order123"
	return order, nil
}

func (m *mockExchange) CancelAllOrders(ctx context.Context, symbol string) error {
	// 模拟成功，不做实际操作
	return nil
}

func (m *mockExchange) GetPosition(ctx context.Context, symbol string) (*gateway.Position, error) {
	return nil, nil
}

func (m *mockExchange) GetAllPositions(ctx context.Context) ([]*gateway.Position, error) {
	return nil, nil
}

func (m *mockExchange) GetFundingRate(ctx context.Context, symbol string) (*gateway.FundingRate, error) {
	return nil, nil
}

func (m *mockExchange) GetDepth(ctx context.Context, symbol string, limit int) (*gateway.Depth, error) {
	return nil, nil
}

func (m *mockExchange) Connect(ctx context.Context) error { return nil }
func (m *mockExchange) Disconnect() error                 { return nil }
func (m *mockExchange) IsConnected() bool                 { return true }

func (m *mockExchange) StartDepthStream(ctx context.Context, symbols []string, callback func(depth *gateway.Depth)) error {
	return nil
}

func (m *mockExchange) StartUserStream(ctx context.Context, callbacks *gateway.UserStreamCallbacks) error {
	return nil
}

func assertEqual(t *testing.T, a, b interface{}, msg string) {
	if a != b {
		t.Fatalf("断言失败: %s, got %v, want %v", msg, a, b)
	}
}

func assertLen(t *testing.T, a interface{}, length int, msg string) {
	switch v := a.(type) {
	case []interface{}:
		if len(v) != length {
			t.Fatalf("断言失败: %s, got %d, want %d", msg, len(v), length)
		}
	case []*gateway.Order:
		if len(v) != length {
			t.Fatalf("断言失败: %s, got %d, want %d", msg, len(v), length)
		}
	case []string:
		if len(v) != length {
			t.Fatalf("断言失败: %s, got %d, want %d", msg, len(v), length)
		}
	default:
		t.Fatalf("断言失败: %s, 无法获取长度", msg)
	}
}

// TestSyncActiveOrders 测试同步活跃订单
func TestSyncActiveOrders(t *testing.T) {
	mockEx := &mockExchange{
		openOrders: map[string][]*gateway.Order{
			"BTCUSDT": {
				{ClientOrderID: "c1", Side: "BUY", Quantity: 1.0, FilledQty: 0.2},
				{ClientOrderID: "c2", Side: "SELL", Quantity: 2.0, FilledQty: 1.0},
			},
		},
	}
	store := store.NewStore("", time.Minute)
	store.InitSymbol("BTCUSDT", 100)
	om := NewOrderManager(store, mockEx)

	err := om.SyncActiveOrders(context.Background(), "BTCUSDT")
	if err != nil {
		t.Fatalf("SyncActiveOrders错误: %v", err)
	}

	om.mu.RLock()
	defer om.mu.RUnlock()
	assertLen(t, om.activeOrders["BTCUSDT"], 2, "activeOrders 长度")
	assertEqual(t, store.GetSymbolState("BTCUSDT").PendingBuy, 0.8, "PendingBuy")
	assertEqual(t, store.GetSymbolState("BTCUSDT").PendingSell, 1.0, "PendingSell")
}

// TestCalculateOrderDiff 测试智能订单差分算法
func TestCalculateOrderDiff(t *testing.T) {
	mockEx := &mockExchange{}
	store := store.NewStore("", time.Minute)
	store.InitSymbol("BTCUSDT", 100)
	om := NewOrderManager(store, mockEx)

	// 场景1: 当前订单与期望订单不同价格，应该全部撤销并重新下单
	om.mu.Lock()
	om.activeOrders["BTCUSDT"] = []*gateway.Order{
		{ClientOrderID: "c1", Side: "BUY", Price: 100.0, Quantity: 1.0},
		{ClientOrderID: "c2", Side: "SELL", Price: 101.0, Quantity: 1.0},
	}
	om.mu.Unlock()

	desiredBuy := []*gateway.Order{
		{ClientOrderID: "new1", Side: "BUY", Price: 99.0, Quantity: 1.0},
	}
	desiredSell := []*gateway.Order{
		{ClientOrderID: "new2", Side: "SELL", Price: 102.0, Quantity: 1.0},
	}

	toCancel, toPlace := om.CalculateOrderDiff("BTCUSDT", desiredBuy, desiredSell, 0.01)

	if len(toCancel) != 2 {
		t.Fatalf("场景1: 期望撤销2个订单，实际%d个", len(toCancel))
	}
	if len(toPlace) != 2 {
		t.Fatalf("场景1: 期望新下2个订单，实际%d个", len(toPlace))
	}

	// 场景2: 当前订单与期望订单价格相同但数量不同，应该撤销并重新下单
	om.mu.Lock()
	om.activeOrders["BTCUSDT"] = []*gateway.Order{
		{ClientOrderID: "c3", Side: "BUY", Price: 100.0, Quantity: 1.0},
	}
	om.mu.Unlock()

	desiredBuy2 := []*gateway.Order{
		{Side: "BUY", Price: 100.0, Quantity: 2.0}, // 数量不同
	}

	toCancel2, toPlace2 := om.CalculateOrderDiff("BTCUSDT", desiredBuy2, nil, 0.01)

	if len(toCancel2) != 1 {
		t.Fatalf("场景2: 期望撤销1个订单（数量变化），实际%d个", len(toCancel2))
	}
	if len(toPlace2) != 1 {
		t.Fatalf("场景2: 期望新下1个订单，实际%d个", len(toPlace2))
	}

	// 场景3: 当前订单与期望订单完全匹配，不应该有任何操作
	om.mu.Lock()
	om.activeOrders["BTCUSDT"] = []*gateway.Order{
		{ClientOrderID: "c4", Side: "BUY", Price: 100.0, Quantity: 1.0},
		{ClientOrderID: "c5", Side: "SELL", Price: 101.0, Quantity: 1.0},
	}
	om.mu.Unlock()

	desiredBuy3 := []*gateway.Order{
		{Side: "BUY", Price: 100.0, Quantity: 1.0},
	}
	desiredSell3 := []*gateway.Order{
		{Side: "SELL", Price: 101.0, Quantity: 1.0},
	}

	toCancel3, toPlace3 := om.CalculateOrderDiff("BTCUSDT", desiredBuy3, desiredSell3, 0.01)

	if len(toCancel3) != 0 {
		t.Fatalf("场景3: 订单完全匹配，不应撤销订单，实际撤销%d个", len(toCancel3))
	}
	if len(toPlace3) != 0 {
		t.Fatalf("场景3: 订单完全匹配，不应新下订单，实际新下%d个", len(toPlace3))
	}
}

// TestApplyDiff 测试下单和撤单操作
func TestApplyDiff(t *testing.T) {
	mockEx := &mockExchange{}
	store := store.NewStore("", time.Minute)
	om := NewOrderManager(store, mockEx)

	toCancel := []string{"c1", "c2"}
	toPlace := []*gateway.Order{
		{ClientOrderID: "new1", Symbol: "BTCUSDT", Side: "BUY"},
	}

	// 测试成功调用
	err := om.ApplyDiff(context.Background(), "BTCUSDT", toCancel, toPlace)
	if err != nil {
		t.Fatalf("ApplyDiff错误:%v", err)
	}
	if len(mockEx.cancelCalls) != len(toCancel) {
		t.Fatalf("ApplyDiff撤单调用数量不符")
	}
	if len(mockEx.placeCalls) != len(toPlace) {
		t.Fatalf("ApplyDiff下单调用数量不符")
	}

	// 测试撤单失败仍继续
	mockEx.failCancel = true
	err = om.ApplyDiff(context.Background(), "BTCUSDT", toCancel, toPlace)
	if err != nil {
		t.Fatalf("ApplyDiff错误:%v", err)
	}

	// 测试下单失败仍继续
	mockEx.failCancel = false
	mockEx.failPlace = true
	err = om.ApplyDiff(context.Background(), "BTCUSDT", toCancel, toPlace)
	if err != nil {
		t.Fatalf("ApplyDiff错误:%v", err)
	}
}
