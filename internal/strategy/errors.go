package strategy

import "errors"

var (
	// ErrSymbolNotConfigured 交易对未配置
	ErrSymbolNotConfigured = errors.New("symbol not configured")

	// ErrSymbolNotInitialized 交易对未初始化
	ErrSymbolNotInitialized = errors.New("symbol not initialized")

	// ErrInvalidMidPrice 无效的中间价
	ErrInvalidMidPrice = errors.New("invalid mid price")

	// ErrQuoteFlicker 报价闪烁（撤单频率过高）
	ErrQuoteFlicker = errors.New("quote flicker detected: cancel rate too high")

	// ErrHighVPINToxicity VPIN毒性过高，暂停报价
	ErrHighVPINToxicity = errors.New("high VPIN toxicity detected: pausing quotes")

	// ErrInvalidTradeQuantity 无效的交易数量
	ErrInvalidTradeQuantity = errors.New("invalid trade quantity")
)
