package gateway

import (
	"context"
	"time"
)

// Order represents a trading order
// 文档规范: Order 结构
type Order struct {
	Symbol        string    `json:"symbol"`
	Side          string    `json:"side"` // "BUY" or "SELL"
	Type          string    `json:"type"` // "LIMIT", "MARKET"
	Quantity      float64   `json:"quantity"`
	Price         float64   `json:"price"`
	ClientOrderID string    `json:"clientOrderId"` // phoenix-{symbol}-{timestamp}-{seq}
	Status        string    `json:"status"`        // "NEW", "FILLED", "CANCELED"
	FilledQty     float64   `json:"filledQty"`
	CreatedAt     time.Time `json:"createdAt"`
}

// Position represents a trading position
// 文档规范: Position 结构
type Position struct {
	Symbol           string  `json:"symbol"`
	Size             float64 `json:"size"` // Positive for long, negative for short
	EntryPrice       float64 `json:"entryPrice"`
	UnrealizedPNL    float64 `json:"unrealizedPnl"`
	Notional         float64 `json:"notional"` // abs(size) * entryPrice
	Leverage         float64 `json:"leverage"`
	LiquidationPrice float64 `json:"liquidationPrice"`
}

// Fill represents an order fill event
type Fill struct {
	Symbol    string    `json:"symbol"`
	Side      string    `json:"side"`
	Quantity  float64   `json:"quantity"`
	Price     float64   `json:"price"`
	Fee       float64   `json:"fee"`
	PNL       float64   `json:"pnl"`
	Timestamp time.Time `json:"timestamp"`
	OrderID   string    `json:"orderId"`
}

// Depth represents order book depth
type Depth struct {
	Symbol    string       `json:"symbol"`
	Bids      []PriceLevel `json:"bids"` // [[price, quantity], ...]
	Asks      []PriceLevel `json:"asks"`
	Timestamp time.Time    `json:"timestamp"`
}

// PriceLevel represents a price level in the order book
type PriceLevel struct {
	Price    float64 `json:"price"`
	Quantity float64 `json:"quantity"`
}

// FundingRate represents funding rate information
type FundingRate struct {
	Symbol          string    `json:"symbol"`
	Rate            float64   `json:"rate"`
	NextFundingTime time.Time `json:"nextFundingTime"`
	Timestamp       time.Time `json:"timestamp"`
}

// Exchange interface defines the contract for exchange operations
// 文档规范: Exchange 接口
type Exchange interface {
	// Order management
	PlaceOrder(ctx context.Context, order *Order) (*Order, error)
	CancelOrder(ctx context.Context, symbol, clientOrderID string) error
	CancelAllOrders(ctx context.Context, symbol string) error
	GetOpenOrders(ctx context.Context, symbol string) ([]*Order, error)

	// Position management
	GetPosition(ctx context.Context, symbol string) (*Position, error)
	GetAllPositions(ctx context.Context) ([]*Position, error)
	GetAccountBalance(ctx context.Context) (float64, float64, error) // TotalWalletBalance, TotalUnrealizedPNL

	// Market data
	GetFundingRate(ctx context.Context, symbol string) (*FundingRate, error)
	GetDepth(ctx context.Context, symbol string, limit int) (*Depth, error)

	// WebSocket streams
	StartDepthStream(ctx context.Context, symbols []string, callback func(*Depth)) error
	StartUserStream(ctx context.Context, callbacks *UserStreamCallbacks) error
	ReconnectStreams(ctx context.Context) error

	// Connection management
	Connect(ctx context.Context) error
	Disconnect() error
	IsConnected() bool
}

// UserStreamCallbacks defines callbacks for user stream events
// 文档规范: WSS callbacks
type UserStreamCallbacks struct {
	OnOrderUpdate   func(*Order)
	OnAccountUpdate func([]*Position)
	OnFunding       func(*FundingRate)
}

// ExchangeError represents an exchange-specific error
type ExchangeError struct {
	Code    int    `json:"code"`
	Message string `json:"msg"`
}

func (e *ExchangeError) Error() string {
	return e.Message
}

// Common errors
var (
	ErrRateLimit    = &ExchangeError{Code: 429, Message: "rate limit exceeded"}
	ErrInvalidOrder = &ExchangeError{Code: 400, Message: "invalid order"}
	ErrNotConnected = &ExchangeError{Code: 503, Message: "not connected"}
	ErrTimeout      = &ExchangeError{Code: 504, Message: "request timeout"}
)
