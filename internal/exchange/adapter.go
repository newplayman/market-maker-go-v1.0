package gateway

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/newplayman/market-maker-phoenix/internal/metrics"
	"github.com/rs/zerolog/log"
)

// BinanceAdapter implements Exchange interface
type BinanceAdapter struct {
	rest            BinanceREST
	restClient      *BinanceRESTClient // 直接访问REST客户端以调用OpenOrders等方法
	ws              BinanceWS
	tradeWS         *TradeWSClient // WebSocket trading client
	listenKeyClient *ListenKeyClient
	connected       bool
	wsStarted       bool // WebSocket是否已启动
	mu              sync.RWMutex

	// Callbacks
	depthCallback func(*Depth)
	userCallbacks *UserStreamCallbacks

	// State
	positions  map[string]*Position
	orders     map[string]*Order // key: clientOrderID -> Order
	orderIDMap map[string]int64  // key: clientOrderID -> exchange orderId (数字)
	stateMu    sync.RWMutex

	// Store reference for VPIN updates
	store interface{} // *store.Store，使用interface{}避免循环依赖

	// ListenKey management
	currentListenKey string
	listenKeyCtx     context.Context
	listenKeyCancel  context.CancelFunc

	// WebSocket reconnection context
	wsCtx context.Context // 用于WebSocket重连的context
}

// NewBinanceAdapter creates a new Binance exchange adapter
func NewBinanceAdapter(rest BinanceREST, ws BinanceWS) *BinanceAdapter {
	// Initialize WebSocket trading client
	// Get API keys from REST client if it's BinanceRESTClient
	apiKey := ""
	secretKey := ""
	var listenKeyClient *ListenKeyClient

	if restClient, ok := rest.(*BinanceRESTClient); ok {
		apiKey = restClient.APIKey
		secretKey = restClient.Secret

		// Initialize ListenKey client
		listenKeyClient = &ListenKeyClient{
			BaseURL:    restClient.BaseURL,
			APIKey:     apiKey,
			HTTPClient: NewListenKeyHTTPClient(),
		}
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

	adapter := &BinanceAdapter{
		rest:            rest,
		ws:              ws,
		tradeWS:         tradeWS,
		listenKeyClient: listenKeyClient,
		positions:       make(map[string]*Position),
		orders:          make(map[string]*Order),
		orderIDMap:      make(map[string]int64),
	}

	// 保存REST客户端引用以便调用OpenOrders等方法
	if restClient, ok := rest.(*BinanceRESTClient); ok {
		adapter.restClient = restClient
	}

	return adapter
}

// PlaceOrder places a new order via REST API (fallback from WSS)
func (b *BinanceAdapter) PlaceOrder(ctx context.Context, order *Order) (*Order, error) {
	if order == nil {
		return nil, ErrInvalidOrder
	}

	// Generate client order ID if not provided
	if order.ClientOrderID == "" {
		// 使用Unix毫秒时间戳确保订单ID长度符合交易所要求(小于36字符)
		order.ClientOrderID = fmt.Sprintf("phoenix-%s-%d", order.Symbol, time.Now().UnixMilli())
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

	// 撤单成功后，从本地订单map中删除该订单
	b.stateMu.Lock()
	if order, exists := b.orders[clientOrderID]; exists {
		order.Status = "CANCELED"
		delete(b.orders, clientOrderID)
	}
	delete(b.orderIDMap, clientOrderID)
	b.stateMu.Unlock()

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

// GetOpenOrders returns all open orders for a symbol from exchange REST API
func (b *BinanceAdapter) GetOpenOrders(ctx context.Context, symbol string) ([]*Order, error) {
	// 优先使用REST API获取真实的交易所订单列表
	if b.restClient != nil {
		exchangeOrders, err := b.restClient.OpenOrders(symbol)
		if err != nil {
			log.Error().Err(err).Str("symbol", symbol).Msg("REST获取活跃订单失败，回退到本地缓存")
			// 回退到本地缓存
			return b.getLocalOpenOrders(symbol), nil
		}

		// 将交易所订单转换为内部Order结构，并同步到本地缓存
		orders := make([]*Order, 0, len(exchangeOrders))
		b.stateMu.Lock()
		// 清空该symbol的旧订单
		for clientID, order := range b.orders {
			if order.Symbol == symbol {
				delete(b.orders, clientID)
				delete(b.orderIDMap, clientID)
			}
		}
		// 添加从交易所获取的订单
		for _, eo := range exchangeOrders {
			order := &Order{
				Symbol:        eo.Symbol,
				Side:          eo.Side,
				Type:          eo.OrderType,
				Status:        eo.Status,
				ClientOrderID: eo.ClientOrderID,
				Price:         eo.Price,
				Quantity:      eo.OrigQty,
				FilledQty:     eo.ExecutedQty,
			}
			orders = append(orders, order)
			// 同步到本地缓存
			b.orders[eo.ClientOrderID] = order
			b.orderIDMap[eo.ClientOrderID] = eo.OrderID
		}
		b.stateMu.Unlock()

		log.Debug().
			Str("symbol", symbol).
			Int("count", len(orders)).
			Msg("从交易所同步活跃订单")

		return orders, nil
	}

	// 如果没有REST客户端，回退到本地缓存
	return b.getLocalOpenOrders(symbol), nil
}

// getLocalOpenOrders 从本地缓存获取活跃订单
func (b *BinanceAdapter) getLocalOpenOrders(symbol string) []*Order {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()

	var orders []*Order
	for _, order := range b.orders {
		if order.Symbol == symbol && order.Status == "NEW" {
			orders = append(orders, order)
		}
	}
	return orders
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

	// Start WebSocket if not already started
	b.startWebSocketIfReady()

	return nil
}

// StartUserStream starts the user data stream
func (b *BinanceAdapter) StartUserStream(ctx context.Context, callbacks *UserStreamCallbacks) error {
	b.mu.Lock()
	b.userCallbacks = callbacks
	b.mu.Unlock()

	// Get real listenKey from Binance
	var listenKey string
	if b.listenKeyClient != nil {
		var err error
		listenKey, err = b.listenKeyClient.NewListenKey()
		if err != nil {
			return fmt.Errorf("failed to get listenKey: %w", err)
		}
		log.Info().Msg("成功获取 listenKey")

		// Save listenKey
		b.mu.Lock()
		b.currentListenKey = listenKey
		b.mu.Unlock()

		// Start listenKey refresh goroutine
		b.startListenKeyRefresh(ctx, listenKey)
	} else {
		log.Warn().Msg("ListenKey 客户端未初始化，使用 dummy key")
		listenKey = "dummy-listen-key"
	}

	// Subscribe to user data stream
	if err := b.ws.SubscribeUserData(listenKey); err != nil {
		return err
	}

	log.Info().Msg("用户数据流已订阅")

	// Start WebSocket if not already started
	b.startWebSocketIfReady()

	return nil
}

// startListenKeyRefresh starts a goroutine to refresh listenKey every 30 minutes
func (b *BinanceAdapter) startListenKeyRefresh(ctx context.Context, listenKey string) {
	// Cancel any existing refresh goroutine
	b.mu.Lock()
	if b.listenKeyCancel != nil {
		b.listenKeyCancel()
	}
	b.listenKeyCtx, b.listenKeyCancel = context.WithCancel(ctx)
	b.mu.Unlock()

	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-b.listenKeyCtx.Done():
				return
			case <-ticker.C:
				if err := b.listenKeyClient.KeepAlive(listenKey); err != nil {
					log.Error().Err(err).Msg("刷新 listenKey 失败")
				} else {
					log.Debug().Msg("listenKey 刷新成功")
				}
			}
		}
	}()
}

// Connect establishes connection to the exchange
func (b *BinanceAdapter) Connect(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.connected {
		return nil
	}

	// 【修复断流】保存context用于WebSocket重连
	b.wsCtx = ctx

	// Start WebSocket trading client
	b.tradeWS.Start(ctx)
	log.Info().Msg("WebSocket交易客户端已启动")

	b.connected = true
	log.Info().Msg("Exchange已连接")

	// Note: WebSocket for market data will be started after subscriptions are set up
	return nil
}

// startWebSocketIfReady starts the WebSocket connection after subscriptions are ready
// 【修复断流】设置OnDisconnect回调，自动触发重连
func (b *BinanceAdapter) startWebSocketIfReady() {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Only start WebSocket once
	if b.wsStarted {
		return
	}

	b.wsStarted = true

	// 【修复断流】设置OnDisconnect回调，自动触发重连
	b.ws.OnDisconnect(func(err error) {
		log.Warn().Err(err).Msg("WebSocket断开连接")

		// 【Goroutine泄漏修复】不再重置wsStarted!
		// 原来的逻辑会导致每次重连都启动新goroutine
		// 现在让第一次启动的goroutine永久运行
		// binance_ws_real.go内部会自动重连

		log.Debug().Msg("【泄漏修复】WebSocket断开,依赖内部重连机制,不重启goroutine")
	})

	// Start WebSocket handler for market data
	go func() {
		handler := &adapterWSHandler{
			adapter:      b,
			lastMidPrice: make(map[string]float64),
		}
		if err := b.ws.Run(handler); err != nil {
			log.Error().Err(err).Msg("WebSocket运行错误")

			// Mark as not started so it can be retried
			b.mu.Lock()
			b.wsStarted = false
			b.mu.Unlock()
		}
	}()

	log.Info().Msg("WebSocket市场数据流已启动")
}

// Disconnect closes the connection
func (b *BinanceAdapter) Disconnect() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Cancel listenKey refresh
	if b.listenKeyCancel != nil {
		b.listenKeyCancel()
		b.listenKeyCancel = nil
	}

	// Close listenKey
	if b.listenKeyClient != nil && b.currentListenKey != "" {
		if err := b.listenKeyClient.CloseListenKey(b.currentListenKey); err != nil {
			log.Warn().Err(err).Msg("关闭 listenKey 失败")
		} else {
			log.Info().Msg("listenKey 已关闭")
		}
		b.currentListenKey = ""
	}

	// Close WebSocket trading client
	if b.tradeWS != nil {
		b.tradeWS.Close()
		log.Info().Msg("WebSocket交易客户端已关闭")
	}

	b.connected = false
	b.wsStarted = false
	log.Info().Msg("Exchange已断开")

	return nil
}

// ReconnectStreams reconnects WebSocket streams
func (b *BinanceAdapter) ReconnectStreams(ctx context.Context) error {
	log.Warn().Msg("正在重连 WebSocket 流...")

	b.mu.Lock()
	wsStarted := b.wsStarted
	// 【修复Goroutine泄漏】不要重置wsStarted！
	// b.wsStarted = false
	b.mu.Unlock()

	// Only attempt reconnect if WebSocket was started
	if !wsStarted {
		log.Debug().Msg("WebSocket 未启动，跳过重连")
		return nil
	}

	// 如果WebSocket已经启动，说明内部循环正在运行（或正在重连）
	// 我们强制关闭连接以触发立即重连（并使用新的ListenKey）
	log.Info().Msg("WebSocket 已在运行，强制关闭连接以触发重连")
	b.ws.CloseConnection()

	// Get new listenKey if available
	if b.listenKeyClient != nil {
		listenKey, err := b.listenKeyClient.NewListenKey()
		if err != nil {
			log.Error().Err(err).Msg("重连时获取 listenKey 失败")
			return fmt.Errorf("failed to get new listenKey: %w", err)
		}

		b.mu.Lock()
		oldListenKey := b.currentListenKey
		b.currentListenKey = listenKey
		b.mu.Unlock()

		// Close old listenKey
		if oldListenKey != "" {
			if err := b.listenKeyClient.CloseListenKey(oldListenKey); err != nil {
				log.Warn().Err(err).Msg("关闭旧 listenKey 失败")
			}
		}

		// Re-subscribe with new listenKey
		if err := b.ws.SubscribeUserData(listenKey); err != nil {
			return fmt.Errorf("failed to re-subscribe user data: %w", err)
		}

		// Restart listenKey refresh
		b.startListenKeyRefresh(ctx, listenKey)
		log.Info().Msg("listenKey 已更新")
	}

	// Restart WebSocket
	// 【修复Goroutine泄漏】不需要重新启动，内部循环会自动使用新的ListenKey
	// b.startWebSocketIfReady()

	log.Info().Msg("WebSocket 流重连完成")
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
	adapter      *BinanceAdapter
	lastMidPrice map[string]float64 // 【流量优化】记录每个交易对的上一次mid价格，用于过滤微小变化
	lastPriceMu  sync.RWMutex
}

// OnRawMessage 处理WebSocket原始消息
func (h *adapterWSHandler) OnRawMessage(msg []byte) {
	// 【流量监控】记录WebSocket接收字节数
	// 注意：启用压缩后，这里的len(msg)是压缩后的大小，实际节省了60-70%
	metrics.RecordWSMessage("global", "total", len(msg))

	// 尝试解析深度数据
	symbol, bid, ask, err := ParseCombinedDepth(msg)
	if err == nil && symbol != "" && bid > 0 && ask > 0 {
		// 按symbol分类记录深度消息
		metrics.RecordWSMessage(symbol, "depth", len(msg))
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
// 【流量优化】添加价格变化过滤，跳过微小变化（<0.01%）
func (h *adapterWSHandler) OnDepth(symbol string, bid, ask float64) {
	mid := (bid + ask) / 2.0

	// 更新lastMidPrice
	h.lastPriceMu.Lock()
	if h.lastMidPrice == nil {
		h.lastMidPrice = make(map[string]float64)
	}
	h.lastMidPrice[symbol] = mid
	h.lastPriceMu.Unlock()

	// 【修复Stale Price】移除"价格变化小跳过回调"的逻辑
	// 原来的逻辑会导致Runner收不到心跳，从而误判为断流
	// 现在无论价格是否变化，都发送回调，由Runner决定是否处理（或仅更新时间戳）

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
	// 转发交易数据到Store进行VPIN计算
	h.adapter.mu.RLock()
	store := h.adapter.store
	h.adapter.mu.RUnlock()

	if store != nil {
		// 创建Trade对象
		trade := struct {
			Symbol    string
			Price     float64
			Quantity  float64
			Timestamp time.Time
			IsBuy     bool
		}{
			Symbol:    symbol,
			Price:     price,
			Quantity:  qty,
			Timestamp: time.Now(),
			IsBuy:     false, // 将由VPIN计算器根据mid price推断
		}

		// 使用类型断言更新Store
		if s, ok := store.(interface {
			UpdateTrade(symbol string, trade interface{})
		}); ok {
			s.UpdateTrade(symbol, trade)
		}
	}

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

// GetAccountBalance returns the total wallet balance and total unrealized PNL
func (b *BinanceAdapter) GetAccountBalance(ctx context.Context) (float64, float64, error) {
	if b.restClient == nil {
		return 0, 0, fmt.Errorf("rest client not available")
	}

	accountInfo, err := b.restClient.AccountInfo()
	if err != nil {
		return 0, 0, err
	}

	return accountInfo.TotalWalletBalance, accountInfo.TotalUnrealizedProfit, nil
}

// SetStore 设置Store引用（用于VPIN更新）
// store应为*store.Store类型
func (b *BinanceAdapter) SetStore(store interface{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.store = store
}

// SetLeverage sets the leverage for a symbol
func (b *BinanceAdapter) SetLeverage(ctx context.Context, symbol string, leverage int) error {
	if b.restClient == nil {
		return fmt.Errorf("rest client not initialized")
	}
	return b.restClient.SetLeverage(symbol, leverage)
}

// SetMarginType sets the margin type for a symbol (ISOLATED or CROSSED)
func (b *BinanceAdapter) SetMarginType(ctx context.Context, symbol string, marginType string) error {
	if b.restClient == nil {
		return fmt.Errorf("rest client not initialized")
	}
	return b.restClient.SetMarginType(symbol, marginType)
}
