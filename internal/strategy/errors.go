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
)
