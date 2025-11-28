package order

import (
	"context"
	"fmt"
	"sync"

	gateway "github.com/newplayman/market-maker-phoenix/internal/exchange"
	"github.com/newplayman/market-maker-phoenix/internal/store"
	"github.com/rs/zerolog/log"
)

// OrderManager 管理本地订单状态机，做差异计算和订单同步
type OrderManager struct {
	mu       sync.RWMutex
	store    *store.Store
	exchange gateway.Exchange

	activeOrders map[string][]*gateway.Order // symbol -> orders
}

// NewOrderManager 创建OrderManager实例
func NewOrderManager(store *store.Store, exch gateway.Exchange) *OrderManager {
	return &OrderManager{
		store:        store,
		exchange:     exch,
		activeOrders: make(map[string][]*gateway.Order),
	}
}

// SyncActiveOrders 同步当前symbol的活跃订单，从exchange拉取并本地更新
func (om *OrderManager) SyncActiveOrders(ctx context.Context, symbol string) error {
	orders, err := om.exchange.GetOpenOrders(ctx, symbol)
	if err != nil {
		return fmt.Errorf("获取当前持有订单失败: %w", err)
	}

	om.mu.Lock()
	defer om.mu.Unlock()

	om.activeOrders[symbol] = orders

	// 更新挂单量统计，方便store使用
	var buyAmt, sellAmt float64
	for _, order := range orders {
		if order.Side == "BUY" {
			buyAmt += order.Quantity - order.FilledQty
		} else if order.Side == "SELL" {
			sellAmt += order.Quantity - order.FilledQty
		}
	}

	om.store.UpdatePendingOrders(symbol, buyAmt, sellAmt)

	log.Info().
		Str("symbol", symbol).
		Int("active_orders", len(orders)).
		Float64("pending_buy", buyAmt).
		Float64("pending_sell", sellAmt).
		Msg("同步活跃订单")

	return nil
}

// CalculateOrderDiff 根据期望待挂单和当前活跃订单计算差异，返回待撤销订单ID列表和待下新订单切片
func (om *OrderManager) CalculateOrderDiff(
	symbol string,
	desiredBuyQuotes []*gateway.Order,
	desiredSellQuotes []*gateway.Order,
) (toCancel []string, toPlace []*gateway.Order) {
	om.mu.RLock()
	defer om.mu.RUnlock()

	currentOrders := om.activeOrders[symbol]

	// 将当前订单按价格分类
	existingByPrice := make(map[string]*gateway.Order) // key: side_price
	for _, o := range currentOrders {
		key := fmt.Sprintf("%s_%.8f", o.Side, o.Price)
		existingByPrice[key] = o
	}

	// 将期望订单按价格分类
	desiredByPrice := make(map[string]*gateway.Order)
	allDesired := append(desiredBuyQuotes, desiredSellQuotes...)
	for _, o := range allDesired {
		key := fmt.Sprintf("%s_%.8f", o.Side, o.Price)
		desiredByPrice[key] = o
	}

	// 找出需要撤销的订单：存在于current但不在desired中，或价格相同但数量不同
	for key, existingOrder := range existingByPrice {
		desiredOrder, exists := desiredByPrice[key]
		if !exists {
			// 该价格订单不再需要，撤销
			toCancel = append(toCancel, existingOrder.ClientOrderID)
		} else if !ordersMatch(existingOrder, desiredOrder) {
			// 价格相同但数量不同，撤销后重新下单
			toCancel = append(toCancel, existingOrder.ClientOrderID)
		}
	}

	// 找出需要新下的订单：存在于desired但不在current中，或数量不匹配需要重新下单
	for key, desiredOrder := range desiredByPrice {
		existingOrder, exists := existingByPrice[key]
		if !exists {
			// 该价格订单不存在，需要新下
			toPlace = append(toPlace, desiredOrder)
		} else if !ordersMatch(existingOrder, desiredOrder) {
			// 数量不匹配，需要重新下单（已在上面撤销）
			toPlace = append(toPlace, desiredOrder)
		}
		// 如果价格和数量都匹配，保留现有订单，不需要任何操作
	}

	return toCancel, toPlace
}

// ordersMatch 判断两个订单是否匹配（价格和数量都相同）
func ordersMatch(existing, desired *gateway.Order) bool {
	const epsilon = 1e-8
	priceDiff := existing.Price - desired.Price
	if priceDiff < -epsilon || priceDiff > epsilon {
		return false
	}

	qtyDiff := existing.Quantity - desired.Quantity
	if qtyDiff < -epsilon || qtyDiff > epsilon {
		return false
	}

	return true
}

// ApplyDiff 应用订单差分，执行撤单和新单下单
func (om *OrderManager) ApplyDiff(ctx context.Context, symbol string, toCancel []string, toPlace []*gateway.Order) error {
	// 撤单
	for _, orderID := range toCancel {
		if err := om.exchange.CancelOrder(ctx, symbol, orderID); err != nil {
			log.Error().Err(err).Str("order_id", orderID).Msg("撤单失败")
		} else {
			log.Info().Str("order_id", orderID).Msg("撤单成功")
		}
	}

	// 下单
	for _, order := range toPlace {
		if _, err := om.exchange.PlaceOrder(ctx, order); err != nil {
			log.Error().
				Err(err).
				Str("symbol", symbol).
				Str("side", order.Side).
				Float64("price", order.Price).
				Float64("qty", order.Quantity).
				Msg("下单失败")
		} else {
			log.Info().
				Str("symbol", symbol).
				Str("side", order.Side).
				Float64("price", order.Price).
				Float64("qty", order.Quantity).
				Msg("下单成功")
		}
	}

	return nil
}
