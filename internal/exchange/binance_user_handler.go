package gateway

import (
	"errors"
	"log"
)

// BinanceUserHandler 解析用户数据流事件并触发回调。
type BinanceUserHandler struct {
	OnOrderUpdate   func(OrderUpdate)
	OnAccountUpdate func(AccountUpdate)
}

// OnRawMessage 满足 interface { OnRawMessage([]byte) }，可直接交给 BinanceWSReal。
func (h *BinanceUserHandler) OnRawMessage(msg []byte) {
	if h == nil {
		return
	}
	ev, err := ParseUserData(msg)
	if err != nil {
		if errors.Is(err, ErrNonUserData) {
			return
		}
		log.Printf("parse user data err: %v", err)
		return
	}
	switch ev.EventType {
	case "ORDER_TRADE_UPDATE":
		if h.OnOrderUpdate != nil && ev.Order != nil {
			h.OnOrderUpdate(*ev.Order)
		}
	case "ACCOUNT_UPDATE":
		if h.OnAccountUpdate != nil && ev.Account != nil {
			h.OnAccountUpdate(*ev.Account)
		}
	default:
		// 忽略其他事件
	}
}
