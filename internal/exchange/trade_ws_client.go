package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// WSNotificationSink 用于把 WSS 推送事件透传到上层，例如 ORDER_TRADE_UPDATE。
type WSNotificationSink func(method string, payload json.RawMessage)

// TradeFallbackHandler 在 WSS 超时/故障时触发，用于回退到 REST。
type TradeFallbackHandler func(req WSRequestMeta, reason error)

// TradeWSConfig 描述 WSS 交易客户端的必需配置。
type TradeWSConfig struct {
	BaseURL      string
	APIKey       string
	SecretKey    string
	AckTimeout   time.Duration
	Dialer       *websocket.Dialer
	Logger       *log.Logger
	OnNotify     WSNotificationSink
	OnFallback   TradeFallbackHandler
	KeepAlive    time.Duration
	RetryBackoff time.Duration
	MaxRetries   int
}

// TradeWSClient 实现 Binance WebSocket API 的基础下单能力。
// 核心职责：连接、登录、发送 order/cancel、分发 ACK/ERR、在超时时触发回退。
type TradeWSClient struct {
	cfg TradeWSConfig

	connMu sync.Mutex
	conn   *websocket.Conn

	wg      sync.WaitGroup
	ctx     context.Context
	cancel  context.CancelFunc
	started int32

	nextID int64

	pendingMu sync.Mutex
	pending   map[int64]*pendingRequest
}

type pendingRequest struct {
	meta        WSRequestMeta
	respCh      chan tradeResponse
	expireTimer *time.Timer
}

// WSRequestMeta 记录 request 的关键信息，便于 fallback。
type WSRequestMeta struct {
	Method    string
	Params    map[string]interface{}
	ID        int64
	CreatedAt time.Time
}

type wsRequest struct {
	ID     int64                  `json:"id"`
	Method string                 `json:"method"`
	Params map[string]interface{} `json:"params"`
}

type wsResponse struct {
	ID     int64           `json:"id,omitempty"`
	Status int             `json:"status,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *wsError        `json:"error,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type wsError struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type tradeResponse struct {
	result json.RawMessage
	err    error
}

// TradeOrderParams 描述基本下单参数（精简版）。
type TradeOrderParams struct {
	Symbol          string
	Side            string
	Type            string
	Quantity        float64
	Price           float64
	TimeInForce     string
	ClientOrderID   string
	ReduceOnly      bool
	PositionSide    string
	PostOnly        bool
	WorkingType     string
	CallbackRate    float64
	ActivationPrice float64
}

// TradeCancelParams 描述撤单参数。
type TradeCancelParams struct {
	Symbol        string
	OrderID       int64
	ClientOrderID string
}

// TradeCancelAllParams 描述批量撤单请求。
type TradeCancelAllParams struct {
	Symbol string
}

const (
	defaultBaseWSEndpoint = "wss://ws-fapi.binance.com/ws-fapi/v1"
	defaultAckTimeout     = 3 * time.Second
	defaultKeepAlive      = 15 * time.Second
)

// NewTradeWSClient 创建新的 WSS 交易客户端。
func NewTradeWSClient(cfg TradeWSConfig) *TradeWSClient {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseWSEndpoint
	}
	if cfg.AckTimeout <= 0 {
		cfg.AckTimeout = defaultAckTimeout
	}
	if cfg.Dialer == nil {
		cfg.Dialer = &websocket.Dialer{
			Proxy:             websocket.DefaultDialer.Proxy,
			HandshakeTimeout:  websocket.DefaultDialer.HandshakeTimeout,
			ReadBufferSize:    websocket.DefaultDialer.ReadBufferSize,
			WriteBufferSize:   websocket.DefaultDialer.WriteBufferSize,
			EnableCompression: websocket.DefaultDialer.EnableCompression,
		}
	}
	if len(cfg.Dialer.Subprotocols) == 0 {
		cfg.Dialer.Subprotocols = []string{"binary"}
	}
	if cfg.KeepAlive <= 0 {
		cfg.KeepAlive = defaultKeepAlive
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 5
	}
	return &TradeWSClient{
		cfg:     cfg,
		pending: make(map[int64]*pendingRequest),
	}
}

// Start 启动后台 goroutine（惰性连接，首次请求时拨号）。
func (c *TradeWSClient) Start(ctx context.Context) {
	if !atomic.CompareAndSwapInt32(&c.started, 0, 1) {
		return
	}
	c.ctx, c.cancel = context.WithCancel(ctx)
	c.wg.Add(1)
	go c.keepAliveLoop()
}

// Close 关闭连接并终止后台 goroutine。
func (c *TradeWSClient) Close() {
	if c.cancel != nil {
		c.cancel()
	}
	c.connMu.Lock()
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
	c.connMu.Unlock()
	c.wg.Wait()
}

// Healthy 返回当前 WSS 是否可用（连接存在且未出错）。
func (c *TradeWSClient) Healthy() bool {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	return c.conn != nil
}

// PlaceOrder 通过 WSS 下 LIMIT/MARKET 单。
func (c *TradeWSClient) PlaceOrder(ctx context.Context, p TradeOrderParams) (json.RawMessage, error) {
	params := make(map[string]interface{})
	params["symbol"] = strings.ToUpper(p.Symbol)
	params["side"] = strings.ToUpper(p.Side)
	params["type"] = strings.ToUpper(p.Type)
	if p.TimeInForce != "" {
		params["timeInForce"] = strings.ToUpper(p.TimeInForce)
	}
	if p.Quantity > 0 {
		params["quantity"] = formatFloat(p.Quantity)
	}
	if p.Price > 0 {
		params["price"] = formatFloat(p.Price)
	}
	if p.ClientOrderID != "" {
		params["newClientOrderId"] = p.ClientOrderID
	}
	if p.ReduceOnly {
		params["reduceOnly"] = true
	}
	if p.PositionSide != "" {
		params["positionSide"] = strings.ToUpper(p.PositionSide)
	}
	if p.PostOnly {
		params["timeInForce"] = "GTX"
	}
	if p.WorkingType != "" {
		params["workingType"] = strings.ToUpper(p.WorkingType)
	}
	if p.CallbackRate > 0 {
		params["callbackRate"] = formatFloat(p.CallbackRate)
	}
	if p.ActivationPrice > 0 {
		params["activationPrice"] = formatFloat(p.ActivationPrice)
	}
	return c.call(ctx, "order.place", params)
}

// CancelOrder 通过 WSS 撤单。
func (c *TradeWSClient) CancelOrder(ctx context.Context, p TradeCancelParams) (json.RawMessage, error) {
	params := map[string]interface{}{
		"symbol": strings.ToUpper(p.Symbol),
	}
	if p.OrderID > 0 {
		params["orderId"] = p.OrderID
	}
	if p.ClientOrderID != "" {
		params["origClientOrderId"] = p.ClientOrderID
	}
	return c.call(ctx, "order.cancel", params)
}

// CancelAll 通过 WSS 撤掉该合约所有挂单。
func (c *TradeWSClient) CancelAll(ctx context.Context, p TradeCancelAllParams) (json.RawMessage, error) {
	params := map[string]interface{}{
		"symbol": strings.ToUpper(p.Symbol),
	}
	return c.call(ctx, "order.cancelAll", params)
}

// call 是对外部请求的统一入口，负责发起请求并等待 ACK/ERR。
func (c *TradeWSClient) call(ctx context.Context, method string, params map[string]interface{}) (json.RawMessage, error) {
	if atomic.LoadInt32(&c.started) == 0 {
		c.Start(context.Background())
	}
	if err := c.ensureConnection(); err != nil {
		return nil, err
	}
	reqID := atomic.AddInt64(&c.nextID, 1)
	req := wsRequest{
		ID:     reqID,
		Method: c.qualifyMethod(method),
		Params: c.attachSignature(params),
	}
	respCh := make(chan tradeResponse, 1)
	meta := WSRequestMeta{
		Method:    method,
		Params:    params,
		ID:        reqID,
		CreatedAt: time.Now(),
	}
	timer := time.AfterFunc(c.cfg.AckTimeout, func() {
		c.pendingTimeout(reqID, meta)
	})
	c.pendingMu.Lock()
	c.pending[reqID] = &pendingRequest{
		meta:        meta,
		respCh:      respCh,
		expireTimer: timer,
	}
	c.pendingMu.Unlock()
	if err := c.writeJSON(req); err != nil {
		timer.Stop()
		c.removePending(reqID)
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-respCh:
		return resp.result, resp.err
	}
}

func (c *TradeWSClient) pendingTimeout(id int64, meta WSRequestMeta) {
	c.pendingMu.Lock()
	p, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
	if !ok {
		return
	}
	err := fmt.Errorf("ws request %d timeout", id)
	p.respCh <- tradeResponse{nil, err}
	if c.cfg.OnFallback != nil {
		c.cfg.OnFallback(meta, err)
	}
}

func (c *TradeWSClient) removePending(id int64) {
	c.pendingMu.Lock()
	if p, ok := c.pending[id]; ok {
		p.expireTimer.Stop()
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
}

func (c *TradeWSClient) ensureConnection() error {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn != nil {
		return nil
	}
	var conn *websocket.Conn
	var err error
	header := http.Header{}
	if c.cfg.APIKey != "" {
		header.Set("X-MBX-APIKEY", c.cfg.APIKey)
	}
	backoff := c.cfg.RetryBackoff
	for attempt := 0; attempt < c.cfg.MaxRetries; attempt++ {
		var resp *http.Response
		conn, resp, err = c.cfg.Dialer.Dial(c.cfg.BaseURL, header)
		if err == nil {
			break
		}
		if resp != nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			log.Printf("trade ws handshake failed (%d/%d): %s - %s", attempt+1, c.cfg.MaxRetries, resp.Status, strings.TrimSpace(string(body)))
		}
		time.Sleep(backoff)
		backoff *= 2
	}
	if err != nil {
		return fmt.Errorf("ws dial failed: %w", err)
	}
	if err := c.login(conn); err != nil {
		_ = conn.Close()
		return err
	}
	c.conn = conn
	c.wg.Add(1)
	go c.readLoop(conn)
	return nil
}

func (c *TradeWSClient) login(conn *websocket.Conn) error {
	timestamp := time.Now().UnixMilli()
	// 生成签名: 按参数名排序后拼接 apiKey={key}&timestamp={ts}
	query := url.Values{}
	query.Set("apiKey", c.cfg.APIKey)
	query.Set("timestamp", fmt.Sprintf("%d", timestamp))
	signature := c.sign(query.Encode())
	
	params := map[string]interface{}{
		"apiKey":    c.cfg.APIKey,
		"timestamp": timestamp,
		"signature": signature,
	}
	req := wsRequest{
		ID:     atomic.AddInt64(&c.nextID, 1),
		Method: "session.logon",
		Params: params,
	}
	if err := conn.WriteJSON(req); err != nil {
		return fmt.Errorf("ws login send: %w", err)
	}
	// 等待一次 ACK
	_, message, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("ws login ack read: %w", err)
	}
	var resp wsResponse
	if err := json.Unmarshal(message, &resp); err != nil {
		return fmt.Errorf("ws login ack parse: %w", err)
	}
	if resp.Status != 200 && resp.Error != nil {
		return fmt.Errorf("ws login failed: %d %s", resp.Error.Code, resp.Error.Msg)
	}
	return nil
}

func (c *TradeWSClient) writeJSON(payload interface{}) error {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn == nil {
		return fmt.Errorf("ws not connected")
	}
	return c.conn.WriteJSON(payload)
}

func (c *TradeWSClient) readLoop(conn *websocket.Conn) {
	defer c.wg.Done()
	defer func() {
		c.connMu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.connMu.Unlock()
	}()
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			c.failAllPending(err)
			return
		}
		var resp wsResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			log.Printf("trade ws decode err: %v", err)
			continue
		}
		if resp.Method != "" {
			if c.cfg.OnNotify != nil {
				c.cfg.OnNotify(resp.Method, resp.Params)
			}
			continue
		}
		c.handleResponse(resp)
	}
}

func (c *TradeWSClient) handleResponse(resp wsResponse) {
	id := resp.ID
	c.pendingMu.Lock()
	req, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
	if !ok {
		return
	}
	req.expireTimer.Stop()
	if resp.Error != nil {
		req.respCh <- tradeResponse{nil, fmt.Errorf("ws err %d: %s", resp.Error.Code, resp.Error.Msg)}
		return
	}
	if resp.Status != 200 && resp.Status != 0 {
		req.respCh <- tradeResponse{nil, fmt.Errorf("ws status %d", resp.Status)}
		return
	}
	req.respCh <- tradeResponse{resp.Result, nil}
}

func (c *TradeWSClient) failAllPending(err error) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()
	for id, req := range c.pending {
		req.expireTimer.Stop()
		req.respCh <- tradeResponse{nil, err}
		if c.cfg.OnFallback != nil {
			c.cfg.OnFallback(req.meta, err)
		}
		delete(c.pending, id)
	}
}

func (c *TradeWSClient) keepAliveLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.cfg.KeepAlive)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.sendPing()
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *TradeWSClient) sendPing() {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn == nil {
		return
	}
	_ = c.conn.WriteControl(websocket.PingMessage, []byte("keepalive"), time.Now().Add(time.Second))
}

func (c *TradeWSClient) attachSignature(params map[string]interface{}) map[string]interface{} {
	if params == nil {
		params = make(map[string]interface{})
	}
	now := time.Now().UnixMilli()
	params["timestamp"] = now
	query := url.Values{}
	for k, v := range params {
		switch vv := v.(type) {
		case string:
			query.Set(k, vv)
		case float64:
			query.Set(k, formatFloat(vv))
		case bool:
			query.Set(k, strconv.FormatBool(vv))
		case int64:
			query.Set(k, fmt.Sprintf("%d", vv))
		case int:
			query.Set(k, fmt.Sprintf("%d", vv))
		}
	}
	params["signature"] = c.sign(query.Encode())
	return params
}

func (c *TradeWSClient) sign(payload string) string {
	mac := hmac.New(sha256.New, []byte(c.cfg.SecretKey))
	_, _ = mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func (c *TradeWSClient) qualifyMethod(method string) string {
	// Binance Futures WebSocket API 不使用 v1/v2 前缀
	// 直接返回方法名即可
	return method
}
