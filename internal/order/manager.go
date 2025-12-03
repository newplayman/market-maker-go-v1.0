package order

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
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

	activeOrders     map[string][]*gateway.Order // symbol -> orders
	maxOrdersPerSide map[string]int              // symbol -> max orders per side
}

// NewOrderManager 创建OrderManager实例
func NewOrderManager(store *store.Store, exch gateway.Exchange) *OrderManager {
	return &OrderManager{
		store:            store,
		exchange:         exch,
		activeOrders:     make(map[string][]*gateway.Order),
		maxOrdersPerSide: make(map[string]int),
	}
}

// SetMaxOrdersPerSide 设置单侧最大订单数限制
func (om *OrderManager) SetMaxOrdersPerSide(symbol string, maxOrders int) {
	om.mu.Lock()
	defer om.mu.Unlock()
	om.maxOrdersPerSide[symbol] = maxOrders
	log.Info().Str("symbol", symbol).Int("max_orders_per_side", maxOrders).Msg("设置单侧最大订单数")
}

// GetMaxOrdersPerSide 获取单侧最大订单数限制
func (om *OrderManager) GetMaxOrdersPerSide(symbol string) int {
	om.mu.RLock()
	defer om.mu.RUnlock()
	if max, ok := om.maxOrdersPerSide[symbol]; ok {
		return max
	}
	return 18 // 默认值
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

	// 更新实际活跃订单数量
	om.store.SetActiveOrderCount(symbol, len(orders))

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
	tolerance float64,
) (toCancel []string, toPlace []*gateway.Order) {
	om.mu.RLock()
	defer om.mu.RUnlock()

	currentOrders := om.activeOrders[symbol]

	// 辅助函数：处理单边订单
	processSide := func(current []*gateway.Order, desired []*gateway.Order) {
		usedDesired := make([]bool, len(desired))

		for _, curr := range current {
			matched := false
			// 寻找最匹配的期望订单
			// 由于网格策略通常价格有序，这里简单遍历即可。
			// 如果有性能问题，可以先排序。但考虑到订单数较少（几十个），遍历很快。
			for i, des := range desired {
				if usedDesired[i] {
					continue
				}
				// 检查价格差异是否在容差范围内 (Hysteresis)
				// 如果 |curr - des| <= tolerance，则认为匹配，不需要移动订单
				diff := math.Abs(curr.Price - des.Price)
				if diff <= tolerance+1e-9 {
					matched = true
					usedDesired[i] = true

					// 价格匹配，检查数量
					if math.Abs(curr.Quantity-des.Quantity) > 1e-8 {
						// 数量变化，必须撤销重挂
						log.Debug().
							Str("id", curr.ClientOrderID).
							Float64("curr_qty", curr.Quantity).
							Float64("des_qty", des.Quantity).
							Msg("数量变化，需更新")
						toCancel = append(toCancel, curr.ClientOrderID)
						toPlace = append(toPlace, des)
					} else {
						// 完全匹配
						log.Debug().
							Str("id", curr.ClientOrderID).
							Float64("curr_price", curr.Price).
							Float64("des_price", des.Price).
							Float64("diff", diff).
							Float64("tolerance", tolerance).
							Msg("订单匹配，保持不变")
					}
					// 否则：完全匹配（价格在容差内，数量一致），保留订单，不做任何操作
					break
				} else {
					// Log mismatch for debugging (optional, might be too noisy)
					// log.Trace().Float64("diff", diff).Float64("tol", tolerance).Msg("价格差异过大")
				}
			}
			if !matched {
				// 当前订单在期望列表中找不到匹配项（价格偏离过大或不再需要） -> 撤销
				toCancel = append(toCancel, curr.ClientOrderID)
			}
		}

		// 添加所有未被匹配的期望订单
		for i, des := range desired {
			if !usedDesired[i] {
				toPlace = append(toPlace, des)
			}
		}
	}

	// 将当前订单按方向分类
	var currBuy, currSell []*gateway.Order
	for _, o := range currentOrders {
		if o.Side == "BUY" {
			currBuy = append(currBuy, o)
		} else {
			currSell = append(currSell, o)
		}
	}

	// 排序函数
	sortOrders := func(orders []*gateway.Order, ascending bool) {
		sort.Slice(orders, func(i, j int) bool {
			if ascending {
				return orders[i].Price < orders[j].Price
			}
			return orders[i].Price > orders[j].Price
		})
	}

	// 对订单进行排序，确保匹配逻辑的稳定性
	// 买单：价格从高到低 (靠近盘口优先)
	sortOrders(currBuy, false)
	sortOrders(desiredBuyQuotes, false)

	// 卖单：价格从低到高 (靠近盘口优先)
	sortOrders(currSell, true)
	sortOrders(desiredSellQuotes, true)

	// 分别处理买单和卖单
	processSide(currBuy, desiredBuyQuotes)
	processSide(currSell, desiredSellQuotes)

	return toCancel, toPlace
}

// ordersMatch 判断两个订单是否匹配（价格和数量都相同）
// 引入tolerance作为滞后容差(Hysteresis)，避免微小价格波动导致的频繁撤单
func ordersMatch(existing, desired *gateway.Order, tolerance float64) bool {
	// 容差设为 tolerance (处理浮点误差)
	// 如果差异 <= tolerance，则认为匹配（不更新）
	threshold := tolerance
	if threshold <= 0 {
		threshold = 1e-8
	}

	// 检查价格差异是否在容差范围内
	priceDiff := math.Abs(existing.Price - desired.Price)
	if priceDiff > threshold+1e-9 {
		return false
	}

	qtyDiff := existing.Quantity - desired.Quantity
	if qtyDiff < -1e-8 || qtyDiff > 1e-8 {
		return false
	}

	return true
}

// removeActiveOrder 从本地活跃订单列表中移除指定订单
func (om *OrderManager) removeActiveOrder(symbol, orderID string) {
	om.mu.Lock()
	defer om.mu.Unlock()

	orders := om.activeOrders[symbol]
	newOrders := make([]*gateway.Order, 0, len(orders))
	for _, o := range orders {
		if o.ClientOrderID != orderID {
			newOrders = append(newOrders, o)
		}
	}
	om.activeOrders[symbol] = newOrders
}

// ApplyDiff 应用订单差分，执行撤单和新单下单
func (om *OrderManager) ApplyDiff(ctx context.Context, symbol string, toCancel []string, toPlace []*gateway.Order) error {
	// 【关键修复】限制下单数量，防止订单爆炸
	maxPerSide := om.GetMaxOrdersPerSide(symbol)

	// 统计当前活跃订单数
	om.mu.RLock()
	currentOrders := om.activeOrders[symbol]
	var currentBuyCount, currentSellCount int
	for _, o := range currentOrders {
		if o.Side == "BUY" {
			currentBuyCount++
		} else {
			currentSellCount++
		}
	}
	om.mu.RUnlock()

	// 计算撤单后的订单数
	canceledBuy, canceledSell := 0, 0
	for _, orderID := range toCancel {
		om.mu.RLock()
		for _, o := range currentOrders {
			if o.ClientOrderID == orderID {
				if o.Side == "BUY" {
					canceledBuy++
				} else {
					canceledSell++
				}
				break
			}
		}
		om.mu.RUnlock()
	}

	afterCancelBuy := currentBuyCount - canceledBuy
	afterCancelSell := currentSellCount - canceledSell

	// 撤单
	cancelSuccess := 0
	for _, orderID := range toCancel {
		if err := om.exchange.CancelOrder(ctx, symbol, orderID); err != nil {
			// 如果是订单不存在错误，视为撤单成功（实际上是清理僵尸订单）
			if strings.Contains(err.Error(), "Unknown order") || strings.Contains(err.Error(), "-2011") {
				log.Warn().Str("order_id", orderID).Msg("订单不存在，从本地状态移除")
				om.removeActiveOrder(symbol, orderID)
				// 不增加 cancelCount，因为这不是一次有效的撤单消耗
			} else {
				log.Error().Err(err).Str("order_id", orderID).Msg("撤单失败")
			}
		} else {
			// 【关键修复】撤单成功后调用计数器
			om.store.IncrementCancelCount(symbol)
			cancelSuccess++
			log.Info().Str("order_id", orderID).Msg("撤单成功")
		}
	}

	// 分离买单和卖单
	var buyOrders, sellOrders []*gateway.Order
	for _, order := range toPlace {
		if order.Side == "BUY" {
			buyOrders = append(buyOrders, order)
		} else {
			sellOrders = append(sellOrders, order)
		}
	}

	// 【关键修复】限制每侧下单数量
	allowedBuy := maxPerSide - afterCancelBuy
	allowedSell := maxPerSide - afterCancelSell

	if allowedBuy < 0 {
		allowedBuy = 0
	}
	if allowedSell < 0 {
		allowedSell = 0
	}

	if len(buyOrders) > allowedBuy {
		log.Warn().
			Str("symbol", symbol).
			Int("desired", len(buyOrders)).
			Int("allowed", allowedBuy).
			Int("max_per_side", maxPerSide).
			Msg("买单数量超限，截断")
		buyOrders = buyOrders[:allowedBuy]
	}

	if len(sellOrders) > allowedSell {
		log.Warn().
			Str("symbol", symbol).
			Int("desired", len(sellOrders)).
			Int("allowed", allowedSell).
			Int("max_per_side", maxPerSide).
			Msg("卖单数量超限，截断")
		sellOrders = sellOrders[:allowedSell]
	}

	// 合并订单列表
	limitedOrders := append(buyOrders, sellOrders...)

	// 下单
	placeSuccess := 0
	for _, order := range limitedOrders {
		if _, err := om.exchange.PlaceOrder(ctx, order); err != nil {
			log.Error().
				Err(err).
				Str("symbol", symbol).
				Str("side", order.Side).
				Float64("price", order.Price).
				Float64("qty", order.Quantity).
				Msg("下单失败")
		} else {
			// 【P1-4】下单成功后增加计数
			om.store.IncrementPlaceCount(symbol)
			placeSuccess++
			log.Info().
				Str("symbol", symbol).
				Str("side", order.Side).
				Float64("price", order.Price).
				Float64("qty", order.Quantity).
				Msg("下单成功")
		}
	}

	log.Debug().
		Str("symbol", symbol).
		Int("cancel_requested", len(toCancel)).
		Int("cancel_success", cancelSuccess).
		Int("place_requested", len(toPlace)).
		Int("place_limited", len(limitedOrders)).
		Int("place_success", placeSuccess).
		Msg("订单差分应用完成")

	return nil
}
