package gateway

// BinanceWSStub 是一个占位 WebSocket 客户端实现，便于单测/离线演示。
// Run 不会连接网络；调用时可注入 handler 以模拟推送。
type BinanceWSStub struct {
	depthSym   []string
	userListen string
}

func (b *BinanceWSStub) SubscribeDepth(symbol string) error {
	b.depthSym = append(b.depthSym, symbol)
	return nil
}

func (b *BinanceWSStub) SubscribeUserData(listenKey string) error {
	b.userListen = listenKey
	return nil
}

// Run 模拟一次回调，验证 handler 是否能处理。
func (b *BinanceWSStub) Run(handler WSHandler) error {
	if handler != nil && len(b.depthSym) > 0 {
		handler.OnDepth(b.depthSym[0], 100, 101)
		handler.OnTrade(b.depthSym[0], 100, 1)
	}
	return nil
}

// OnDisconnect 【修复断流】实现接口方法（stub不需要实际处理）
func (b *BinanceWSStub) OnDisconnect(cb func(error)) {
	// Stub实现，不需要实际处理
}

func (b *BinanceWSStub) CloseConnection() {
	// Stub实现，不需要实际处理
}
