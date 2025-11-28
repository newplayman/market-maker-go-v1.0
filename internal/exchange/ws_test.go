package gateway

import "testing"

type stubWS struct{}

func (s *stubWS) SubscribeDepth(symbol string) error { return nil }
func (s *stubWS) SubscribeTrade(symbol string) error { return nil }
func (s *stubWS) Run(handler WSHandler) error {
	handler.OnDepth("BTCUSDT", 100, 101)
	handler.OnTrade("BTCUSDT", 100, 1)
	return nil
}

type stubHandler struct {
	depthCalled bool
	tradeCalled bool
}

func (h *stubHandler) OnDepth(symbol string, bid, ask float64)   { h.depthCalled = true }
func (h *stubHandler) OnTrade(symbol string, price, qty float64) { h.tradeCalled = true }

func TestWSClient(t *testing.T) {
	ws := &stubWS{}
	h := &stubHandler{}
	if err := ws.Run(h); err != nil {
		t.Fatalf("ws run err: %v", err)
	}
	if !h.depthCalled || !h.tradeCalled {
		t.Fatalf("handler not invoked")
	}
}
