package gateway

// Binance endpoints (USDC-M perpetual)
const (
	BinanceFuturesWSEndpoint   = "wss://fstream.binance.com"
	BinanceFuturesRestEndpoint = "https://fapi.binance.com"
)

// BinanceREST is a minimal REST client interface; real实现需签名、时间戳等。
type BinanceREST interface {
	PlaceLimit(symbol, side, tif string, price, qty float64, reduceOnly, postOnly bool, clientID string) (string, error)
	CancelOrder(symbol, orderID string) error
}

// BinanceWS is一个极简抽象，供后续对接 binance ws 客户端。
type BinanceWS interface {
	SubscribeDepth(symbol string) error
	SubscribeUserData(listenKey string) error
	Run(handler WSHandler) error
	OnDisconnect(cb func(error)) // 【修复断流】设置断开连接回调
	CloseConnection()            // 【修复Goroutine泄漏】强制关闭连接
}

// BinanceClient 聚合 REST 与 WS；这里为占位骨架，方便后续替换为真实实现。
type BinanceClient struct {
	rest BinanceREST
	ws   BinanceWS
}

func NewBinanceClient(rest BinanceREST, ws BinanceWS) *BinanceClient {
	return &BinanceClient{rest: rest, ws: ws}
}
