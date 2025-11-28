package gateway

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// BinanceAdapter implements Exchange interface
type BinanceAdapter struct {
	rest      BinanceREST
	ws        BinanceWS
	tradeWS   *TradeWSClient  // WebSocket trading client
	connected bool
	mu        sync.RWMutex

	// Callbacks
	depthCallback func(*Depth)
	userCallbacks *UserStreamCallbacks

	// State
	positions map[string]*Position
	orders    map[string]*Order
	stateMu   sync.RWMutex
}

// NewBinanceAdapter creates a new Binance exchange adapter
func NewBinanceAdapter(rest BinanceREST, ws BinanceWS) *BinanceAdapter {
	// Initialize WebSocket trading client
	// Get API keys from REST client if it's BinanceRESTClient
	apiKey := ""
	secretKey := ""
	if restClient, ok := rest.(*BinanceRESTClient); ok {
		apiKey = restClient.APIKey
		secretKey = restClient.Secret
	}
	
	tradeWS := NewTradeWSClient(TradeWSConfig{
		BaseURL:      "wss://ws-fapi.binance.com/ws-fapi/v1",
		APIKey:       apiKey,
		SecretKey:    secretKey,
		AckTimeout:   3 * time.Second,
		KeepAlive:    15 * time.Second,
		RetryBackoff: time.Second,
		MaxRetries:   5,
	})
	
	return &BinanceAdapter{
		rest:      rest,
		ws:        ws,
		tradeWS:   tradeWS,
		positions: make(map[string]*Position),
		orders:    make(map[string]*Order),
	}
}

// PlaceOrder places a new order via REST API (fallback from WSS)
func (b *BinanceAdapter) PlaceOrder(ctx context.Context, order *Order) (*Order, error) {
	if order == nil {
		return nil, ErrInvalidOrder
	}

	// Generate client order ID if not provided
	if order.ClientOrderID == "" {
		order.ClientOrderID = fmt.Sprintf("phoenix-%s-%d", order.Symbol, time.Now().UnixNano())
	}

	// Use REST API as fallback (WSS requires special API key permissions)
	orderID, err := b.rest.PlaceLimit(
		order.Symbol,
		order.Side,
		"GTC", // Good Till Cancel
		order.Price,
		order.Quantity,
		false, // reduceOnly
		true,  // postOnly - Maker-only for free fees
		order.ClientOrderID,
	)
	
	// If Post Only failed with -5022 error (would execute as taker), skip
	if err != nil && strings.Contains(err.Error(), "-5022") {
		log.Debug().
			Str("symbol", order.Symbol).
			Str("side", order.Side).
			Float64("price", order.Price).
			Msg("订单价格过近，跳过Post Only失败的订单(REST)")
		return nil, err
	}
	
	if err != nil {
		return nil, fmt.Errorf("rest place order failed: %w", err)
	}

	// Update order state
	order.Status = "NEW"
	order.CreatedAt = time.Now()

	b.stateMu.Lock()
	b.orders[order.ClientOrderID] = order
	b.stateMu.Unlock()

	log.Info().
		Str("symbol", order.Symbol).
		Str("side", order.Side).
		Float64("price", order.Price).
		Float64("qty", order.Quantity).
		Str("client_id", order.ClientOrderID).
		Str("order_id", orderID).
		Str("channel", "REST").
		Msg("订单已下达")

	return order, nil
}

// CancelOrder cancels an existing order via REST API (fallback from WSS)
func (b *BinanceAdapter) CancelOrder(ctx context.Context, symbol, clientOrderID string) error {
	if symbol == "" || clientOrderID == "" {
		return ErrInvalidOrder
	}

	// Use REST API as fallback
	if err := b.rest.CancelOrder(symbol, clientOrderID); err != nil {
		return fmt.Errorf("rest cancel order failed: %w", err)
	}

	log.Info().
		Str("symbol", symbol).
		Str("client_id", clientOrderID).
		Str("channel", "REST").
		Msg("订单已撤销")

	return nil
}

// CancelAllOrders cancels all open orders for a symbol
func (b *BinanceAdapter) CancelAllOrders(ctx context.Context, symbol string) error {
	b.stateMu.RLock()
	var orderIDs []string
	for id, order := range b.orders {
		if order.Symbol == symbol && order.Status == "NEW" {
			orderIDs = append(orderIDs, id)
		}
	}
	b.stateMu.RUnlock()

	for _, id := range orderIDs {
		if err := b.rest.CancelOrder(symbol, id); err != nil {
			log.Error().Err(err).Str("order_id", id).Msg("撤单失败")
		}
	}

	log.Info().
		Str("symbol", symbol).
		Int("count", len(orderIDs)).
		Msg("批量撤单完成")

	return nil
}

// GetOpenOrders returns all open orders for a symbol
func (b *BinanceAdapter) GetOpenOrders(ctx context.Context, symbol string) ([]*Order, error) {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()

	var orders []*Order
	for _, order := range b.orders {
		if order.Symbol == symbol && order.Status == "NEW" {
			orders = append(orders, order)
		}
	}

	return orders, nil
}

// GetPosition returns the position for a symbol
func (b *BinanceAdapter) GetPosition(ctx context.Context, symbol string) (*Position, error) {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()

	pos, ok := b.positions[symbol]
	if !ok {
		// Return empty position
		return &Position{
			Symbol: symbol,
			Size:   0,
		}, nil
	}

	return pos, nil
}

// GetAllPositions returns all positions
func (b *BinanceAdapter) GetAllPositions(ctx context.Context) ([]*Position, error) {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()

	positions := make([]*Position, 0, len(b.positions))
	for _, pos := range b.positions {
		positions = append(positions, pos)
	}

	return positions, nil
}

// GetFundingRate returns the funding rate for a symbol
func (b *BinanceAdapter) GetFundingRate(ctx context.Context, symbol string) (*FundingRate, error) {
	// Stub implementation - return zero funding rate
	return &FundingRate{
		Symbol:          symbol,
		Rate:            0.0001, // 0.01%
		NextFundingTime: time.Now().Add(8 * time.Hour),
		Timestamp:       time.Now(),
	}, nil
}

// GetDepth returns order book depth
func (b *BinanceAdapter) GetDepth(ctx context.Context, symbol string, limit int) (*Depth, error) {
	// Stub implementation
	return &Depth{
		Symbol:    symbol,
		Bids:      []PriceLevel{},
		Asks:      []PriceLevel{},
		Timestamp: time.Now(),
	}, nil
}

// StartDepthStream starts the depth stream
func (b *BinanceAdapter) StartDepthStream(ctx context.Context, symbols []string, callback func(*Depth)) error {
	b.mu.Lock()
	b.depthCallback = callback
	b.mu.Unlock()

	// Subscribe to depth streams
	for _, symbol := range symbols {
		if err := b.ws.SubscribeDepth(symbol); err != nil {
			return err
		}
	}

	log.Info().Strs("symbols", symbols).Msg("深度流已订阅")
	return nil
}

// StartUserStream starts the user data stream
func (b *BinanceAdapter) StartUserStream(ctx context.Context, callbacks *UserStreamCallbacks) error {
	b.mu.Lock()
	b.userCallbacks = callbacks
	b.mu.Unlock()

	// Subscribe to user data stream
	listenKey := "dummy-listen-key" // Should be obtained from REST API
	if err := b.ws.SubscribeUserData(listenKey); err != nil {
		return err
	}

	log.Info().Msg("用户数据流已订阅")
	return nil
}

// Connect establishes connection to the exchange
func (b *BinanceAdapter) Connect(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.connected {
		return nil
	}

	// Start WebSocket trading client
	b.tradeWS.Start(ctx)
	log.Info().Msg("WebSocket交易客户端已启动")

	// Start WebSocket handler for market data
	go func() {
		handler := &adapterWSHandler{adapter: b}
		if err := b.ws.Run(handler); err != nil {
			log.Error().Err(err).Msg("WebSocket运行错误")
		}
	}()

	b.connected = true
	log.Info().Msg("Exchange已连接")

	return nil
}

// Disconnect closes the connection
func (b *BinanceAdapter) Disconnect() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Close WebSocket trading client
	if b.tradeWS != nil {
		b.tradeWS.Close()
		log.Info().Msg("WebSocket交易客户端已关闭")
	}

	b.connected = false
	log.Info().Msg("Exchange已断开")

	return nil
}

// IsConnected returns connection status
func (b *BinanceAdapter) IsConnected() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.connected
}

// adapterWSHandler handles WebSocket events
// Implements WSHandler interface from ws.go
type adapterWSHandler struct {
	adapter *BinanceAdapter
}

// OnRawMessage 处理WebSocket原始消息
func (h *adapterWSHandler) OnRawMessage(msg []byte) {
	// 尝试解析深度数据
	symbol, bid, ask, err := ParseCombinedDepth(msg)
	if err == nil && symbol != "" && bid > 0 && ask > 0 {
		h.OnDepth(symbol, bid, ask)
		return
	}

	// 尝试解析用户数据
	userEvent, err := ParseUserData(msg)
	if err != nil {
		if err != ErrNonUserData {
			log.Debug().Err(err).Msg("解析WS消息失败")
		}
		return
	}

	// 处理订单更新
	if userEvent.Order != nil {
		order := &Order{
			Symbol:        userEvent.Order.Symbol,
			Side:          userEvent.Order.Side,
			Type:          userEvent.Order.OrderType,
			Status:        userEvent.Order.Status,
			ClientOrderID: userEvent.Order.ClientOrderID,
			Price:         userEvent.Order.Price,
			Quantity:      userEvent.Order.OrigQty,
			FilledQty:     userEvent.Order.AccumulatedQty,
			CreatedAt:     time.Unix(0, userEvent.Order.EventTime*1e6),
		}
		h.HandleOrderUpdate(order)
	}

	// 处理账户更新
	if userEvent.Account != nil {
		var positions []*Position
		for _, p := range userEvent.Account.Positions {
			positions = append(positions, &Position{
				Symbol:        p.Symbol,
				Size:          p.PositionAmt,
				EntryPrice:    p.EntryPrice,
				UnrealizedPNL: p.PnL,
			})
		}
		if len(positions) > 0 {
			h.HandlePositionUpdate(positions)
		}
	}
}

// OnDepth handles depth updates from WebSocket
func (h *adapterWSHandler) OnDepth(symbol string, bid, ask float64) {
	// Create Depth object
	depth := &Depth{
		Symbol: symbol,
		Bids: []PriceLevel{
			{Price: bid, Quantity: 1.0},
		},
		Asks: []PriceLevel{
			{Price: ask, Quantity: 1.0},
		},
		Timestamp: time.Now(),
	}

	h.adapter.mu.RLock()
	callback := h.adapter.depthCallback
	h.adapter.mu.RUnlock()

	if callback != nil {
		callback(depth)
	}
}

// OnTrade handles trade updates from WebSocket
func (h *adapterWSHandler) OnTrade(symbol string, price, qty float64) {
	// Trade events can be used for additional processing if needed
	log.Debug().
		Str("symbol", symbol).
		Float64("price", price).
		Float64("qty", qty).
		Msg("交易事件")
}

// HandleOrderUpdate handles order updates (called by user stream)
func (h *adapterWSHandler) HandleOrderUpdate(order *Order) {
	h.adapter.stateMu.Lock()
	h.adapter.orders[order.ClientOrderID] = order
	h.adapter.stateMu.Unlock()

	h.adapter.mu.RLock()
	callbacks := h.adapter.userCallbacks
	h.adapter.mu.RUnlock()

	if callbacks != nil && callbacks.OnOrderUpdate != nil {
		callbacks.OnOrderUpdate(order)
	}
}

// HandlePositionUpdate handles position updates (called by user stream)
func (h *adapterWSHandler) HandlePositionUpdate(positions []*Position) {
	h.adapter.stateMu.Lock()
	for _, pos := range positions {
		h.adapter.positions[pos.Symbol] = pos
	}
	h.adapter.stateMu.Unlock()

	h.adapter.mu.RLock()
	callbacks := h.adapter.userCallbacks
	h.adapter.mu.RUnlock()

	if callbacks != nil && callbacks.OnAccountUpdate != nil {
		callbacks.OnAccountUpdate(positions)
	}
}

// HandleFundingUpdate handles funding rate updates (called by user stream)
func (h *adapterWSHandler) HandleFundingUpdate(funding *FundingRate) {
	h.adapter.mu.RLock()
	callbacks := h.adapter.userCallbacks
	h.adapter.mu.RUnlock()

	if callbacks != nil && callbacks.OnFunding != nil {
		callbacks.OnFunding(funding)
	}
}
