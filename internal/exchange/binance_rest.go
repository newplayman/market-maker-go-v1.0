package gateway

import (
	"fmt"
	"time"
)

// BinanceRESTStub 是一个离线占位实现，便于集成测试或本地演示。
// 真实环境应替换为签名 HTTP 请求。
type BinanceRESTStub struct {
	BaseURL string
	APIKey  string
	Secret  string
}

// PlaceLimit 返回一个模拟的 orderID，不发起网络请求。
func (b *BinanceRESTStub) PlaceLimit(symbol, side, tif string, price, qty float64, reduceOnly, postOnly bool, clientID string) (string, error) {
	if symbol == "" || side == "" || price <= 0 || qty <= 0 {
		return "", fmt.Errorf("invalid params")
	}
	if clientID == "" {
		clientID = "cli"
	}
	return fmt.Sprintf("binance-%s-%s-%d", symbol, clientID, time.Now().UnixNano()), nil
}

// CancelOrder 返回 nil，不发起网络请求。
func (b *BinanceRESTStub) CancelOrder(symbol, orderID string) error {
	if symbol == "" || orderID == "" {
		return fmt.Errorf("symbol/orderID required")
	}
	return nil
}

// PlaceMarket 返回一个模拟的 orderID
func (b *BinanceRESTStub) PlaceMarket(symbol, side string, qty float64, reduceOnly bool, clientID string) (string, error) {
	if symbol == "" || side == "" || qty <= 0 {
		return "", fmt.Errorf("invalid params")
	}
	if clientID == "" {
		clientID = "cli"
	}
	return fmt.Sprintf("binance-%s-%s-%d", symbol, clientID, time.Now().UnixNano()), nil
}
