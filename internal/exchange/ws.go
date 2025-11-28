package gateway

// WSHandler 处理 ws 推送。
type WSHandler interface {
	OnDepth(symbol string, bid, ask float64)
	OnTrade(symbol string, price, qty float64)
}

// WSClient 是 ws 连接的抽象。
type WSClient interface {
	SubscribeDepth(symbol string) error
	SubscribeTrade(symbol string) error
	Run(handler WSHandler) error
}
