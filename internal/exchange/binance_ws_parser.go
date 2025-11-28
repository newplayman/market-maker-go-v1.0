package gateway

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// CombinedMessage 对应 binance combined stream 包装。
type CombinedMessage struct {
	Stream string          `json:"stream"`
	Data   json.RawMessage `json:"data"`
}

// ErrNonUserData 表示该 WS 消息不是用户数据流事件，应由调用方静默忽略。
var ErrNonUserData = errors.New("ws message is not user data")

// DepthUpdate 提取 depth@100ms 消息的核心字段。
type DepthUpdate struct {
	EventType interface{}   `json:"e"`
	Symbol    string        `json:"s"`
	Bids      []interface{} `json:"b"`
	Asks      []interface{} `json:"a"`
}

// UserEvent 表示解析后的用户流事件。
type UserEvent struct {
	EventType string
	Order     *OrderUpdate
	Account   *AccountUpdate
}

// OrderUpdate 精简的订单回报。
type OrderUpdate struct {
	Symbol           string
	Side             string
	OrderType        string
	Status           string
	ExecutionType    string
	OrderID          int64
	ClientOrderID    string
	Price            float64
	OrigQty          float64
	LastFilledQty    float64
	AccumulatedQty   float64
	LastFilledPrice  float64
	RealizedPnL      float64
	CommissionAsset  string
	CommissionAmount float64
	PositionSide     string
	EventTime        int64
	UpdateTime       int64
}

// AccountUpdate 精简的资产/仓位更新。
type AccountUpdate struct {
	Reason    string
	Balances  []AccountBalance
	Positions []AccountPosition
}

type AccountBalance struct {
	Asset         string
	WalletBalance float64
	CrossWallet   float64
}

type AccountPosition struct {
	Symbol       string
	PositionAmt  float64
	EntryPrice   float64
	PnL          float64
	MarginType   string
	PositionSide string
}

// ParseCombinedDepth 解析 combined stream 的 depth 消息，返回符号、最好 bid/ask。
func ParseCombinedDepth(raw []byte) (symbol string, bestBid, bestAsk float64, err error) {
	var msg CombinedMessage
	if err = json.Unmarshal(raw, &msg); err != nil {
		return
	}
	var payload map[string]interface{}
	if err = json.Unmarshal(msg.Data, &payload); err != nil {
		return
	}
	if sym, ok := payload["s"].(string); ok {
		symbol = sym
	}
	if bids, ok := payload["b"].([]interface{}); ok && len(bids) > 0 {
		bestBid, _ = parseDepthPrice(bids[0])
	}
	if asks, ok := payload["a"].([]interface{}); ok && len(asks) > 0 {
		bestAsk, _ = parseDepthPrice(asks[0])
	}
	return
}

func parseDepthPrice(entry interface{}) (float64, error) {
	switch v := entry.(type) {
	case []interface{}:
		if len(v) > 0 {
			return toFloat64(v[0]), nil
		}
	case map[string]interface{}:
		if price, ok := v["p"]; ok {
			return toFloat64(price), nil
		}
	}
	return 0, errors.New("unknown depth entry")
}

func toInt64FromInterface(val interface{}) int64 {
	switch v := val.(type) {
	case nil:
		return 0
	case float64:
		return int64(v)
	case string:
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return 0
		}
		return int64(f)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return int64(f)
		}
	}
	return 0
}

// ParseUserData 解析 combined stream 的用户事件。
func ParseUserData(raw []byte) (UserEvent, error) {
	// 1) 先尝试解析 combined stream（含 stream/data 字段）
	var msg CombinedMessage
	if err := json.Unmarshal(raw, &msg); err == nil {
		if msg.Stream != "" && strings.Contains(msg.Stream, "@") {
			return UserEvent{}, ErrNonUserData
		}
		if len(msg.Data) > 0 {
			return parseUserPayload(msg.Data)
		}
	}
	// 2) 兼容普通 listenKey 连接：报文就是订单/账户JSON
	return parseUserPayload(raw)
}

func parseUserPayload(data []byte) (UserEvent, error) {
	var ev UserEvent
	var header map[string]interface{}
	if err := json.Unmarshal(data, &header); err != nil {
		return ev, err
	}
	ev.EventType = toString(header["e"])
	switch ev.EventType {
	case "ORDER_TRADE_UPDATE":
		var payload struct {
			Order struct {
				Symbol           string `json:"s"`
				Side             string `json:"S"`
				OrderType        string `json:"o"`
				Status           string `json:"X"`
				ExecutionType    string `json:"x"`
				OrderID          int64  `json:"i"`
				ClientID         string `json:"c"`
				Price            string `json:"p"`
				OrigQty          string `json:"q"`
				LastQty          string `json:"l"`
				AccumQty         string `json:"z"`
				LastPrice        string `json:"L"`
				RealizedPnL      string `json:"rp"`
				CommissionAsset  string `json:"N"`
				CommissionAmount string `json:"n"`
				PositionSide     string `json:"ps"`
			} `json:"o"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return ev, err
		}
		o := payload.Order
		eventTime := toInt64FromInterface(header["E"])
		var tradeTime int64
		if orderHeader, ok := header["o"].(map[string]interface{}); ok {
			tradeTime = toInt64FromInterface(orderHeader["T"])
		}
		ev.Order = &OrderUpdate{
			Symbol:           o.Symbol,
			Side:             o.Side,
			OrderType:        o.OrderType,
			Status:           o.Status,
			ExecutionType:    o.ExecutionType,
			OrderID:          o.OrderID,
			ClientOrderID:    o.ClientID,
			Price:            parseFloat(o.Price),
			OrigQty:          parseFloat(o.OrigQty),
			LastFilledQty:    parseFloat(o.LastQty),
			AccumulatedQty:   parseFloat(o.AccumQty),
			LastFilledPrice:  parseFloat(o.LastPrice),
			RealizedPnL:      parseFloat(o.RealizedPnL),
			CommissionAsset:  o.CommissionAsset,
			CommissionAmount: parseFloat(o.CommissionAmount),
			PositionSide:     o.PositionSide,
			EventTime:        eventTime,
			UpdateTime:       tradeTime,
		}
	case "ACCOUNT_UPDATE":
		var payload struct {
			Account struct {
				Reason   string `json:"m"`
				Balances []struct {
					Asset  string `json:"a"`
					Wallet string `json:"wb"`
					Cross  string `json:"cw"`
				} `json:"B"`
				Positions []struct {
					Symbol       string `json:"s"`
					PositionAmt  string `json:"pa"`
					EntryPrice   string `json:"ep"`
					PnL          string `json:"cr"`
					MarginType   string `json:"mt"`
					PositionSide string `json:"ps"`
				} `json:"P"`
			} `json:"a"`
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return ev, err
		}
		acc := payload.Account
		au := AccountUpdate{Reason: acc.Reason}
		for _, b := range acc.Balances {
			au.Balances = append(au.Balances, AccountBalance{
				Asset:         b.Asset,
				WalletBalance: parseFloat(b.Wallet),
				CrossWallet:   parseFloat(b.Cross),
			})
		}
		for _, p := range acc.Positions {
			au.Positions = append(au.Positions, AccountPosition{
				Symbol:       p.Symbol,
				PositionAmt:  parseFloat(p.PositionAmt),
				EntryPrice:   parseFloat(p.EntryPrice),
				PnL:          parseFloat(p.PnL),
				MarginType:   p.MarginType,
				PositionSide: p.PositionSide,
			})
		}
		ev.Account = &au
	}
	return ev, nil
}

func parseFloat(v string) float64 {
	if v == "" {
		return 0
	}
	f, _ := strconv.ParseFloat(v, 64)
	return f
}
func toFloat64(v interface{}) float64 {
	switch val := v.(type) {
	case string:
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			return f
		}
	case json.Number:
		if f, err := val.Float64(); err == nil {
			return f
		}
	case float64:
		return val
	}
	return 0
}

func toString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case json.Number:
		return val.String()
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	}
	return ""
}
