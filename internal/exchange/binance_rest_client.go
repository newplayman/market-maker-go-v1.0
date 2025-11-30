package gateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// BinanceRESTClient 一个可签名的简化客户端；默认不发起真实网络调用，HTTPClient 可注入 httptest。
type BinanceRESTClient struct {
	BaseURL      string
	APIKey       string
	Secret       string
	HTTPClient   *http.Client
	RecvWindowMs int64
	Limiter      RateLimiter
	MaxRetries   int
	RetryDelay   time.Duration
	TimeSync     *TimeSync
}

type placeResp struct {
	OrderID json.Number `json:"orderId"`
}

type dualPositionResp struct {
	DualSidePosition bool   `json:"dualSidePosition"`
	Code             int    `json:"code"`
	Msg              string `json:"msg"`
}

type marginTypeResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

type depthResp struct {
	LastUpdateID int64      `json:"lastUpdateId"`
	Bids         [][]string `json:"bids"`
	Asks         [][]string `json:"asks"`
}

// PlaceLimit 调用 /fapi/v1/order 下单（LIMIT）。
func (c *BinanceRESTClient) PlaceLimit(symbol, side, tif string, price, qty float64, reduceOnly, postOnly bool, clientID string) (string, error) {
	if c == nil || c.HTTPClient == nil {
		return "", fmt.Errorf("http client not set")
	}
	if err := validateTimeInForce(tif, postOnly); err != nil {
		return "", err
	}
	params := map[string]string{
		"symbol":   symbol,
		"side":     side,
		"type":     "LIMIT",
		"price":    fmt.Sprintf("%f", price),
		"quantity": fmt.Sprintf("%f", qty),
	}
	if reduceOnly {
		params["reduceOnly"] = "true"
	}
	if postOnly {
		params["timeInForce"] = "GTX"
	} else {
		params["timeInForce"] = tif
	}
	if clientID != "" {
		params["newClientOrderId"] = clientID
	}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/order?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodPost, endpoint, headers)
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("place limit status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var pr placeResp
	if err := json.Unmarshal(body, &pr); err != nil {
		return "", err
	}
	if pr.OrderID == "" {
		return "", fmt.Errorf("empty orderId")
	}
	return pr.OrderID.String(), nil
}

// PlaceMarket 调用 /fapi/v1/order 下市价单（MARKET）。
func (c *BinanceRESTClient) PlaceMarket(symbol, side string, qty float64, reduceOnly bool, clientID string) (string, error) {
	if c == nil || c.HTTPClient == nil {
		return "", fmt.Errorf("http client not set")
	}
	if qty <= 0 {
		return "", fmt.Errorf("qty must be > 0")
	}
	params := map[string]string{
		"symbol":   symbol,
		"side":     side,
		"type":     "MARKET",
		"quantity": fmt.Sprintf("%f", qty),
	}
	if reduceOnly {
		params["reduceOnly"] = "true"
	}
	if clientID != "" {
		params["newClientOrderId"] = clientID
	}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/order?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodPost, endpoint, headers)
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("place market status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var pr placeResp
	if err := json.Unmarshal(body, &pr); err != nil {
		return "", err
	}
	if pr.OrderID == "" {
		return "", fmt.Errorf("empty orderId")
	}
	return pr.OrderID.String(), nil
}

// SetDualPosition 切换持仓模式（true=双向，false=单向）。
func (c *BinanceRESTClient) SetDualPosition(enable bool) error {
	if c == nil || c.HTTPClient == nil {
		return fmt.Errorf("http client not set")
	}
	params := map[string]string{
		"dualSidePosition": strconv.FormatBool(enable),
	}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/positionSide/dual?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodPost, endpoint, headers)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("set position mode status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var pr dualPositionResp
	if err := json.Unmarshal(body, &pr); err != nil {
		return err
	}
	if pr.Code != 0 && pr.Code != 200 {
		return fmt.Errorf("set position mode failed: %d %s", pr.Code, pr.Msg)
	}
	return nil
}

// GetDualPosition 查询当前持仓模式。
func (c *BinanceRESTClient) GetDualPosition() (bool, error) {
	if c == nil || c.HTTPClient == nil {
		return false, fmt.Errorf("http client not set")
	}
	params := map[string]string{}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/positionSide/dual?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodGet, endpoint, headers)
	if err != nil {
		return false, err
	}
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return false, fmt.Errorf("get position mode status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var pr dualPositionResp
	if err := json.Unmarshal(body, &pr); err != nil {
		return false, err
	}
	return pr.DualSidePosition, nil
}

// SetMarginType 切换逐仓/全仓模式。
func (c *BinanceRESTClient) SetMarginType(symbol, marginType string) error {
	if c == nil || c.HTTPClient == nil {
		return fmt.Errorf("http client not set")
	}
	params := map[string]string{
		"symbol":     symbol,
		"marginType": strings.ToUpper(marginType),
	}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/marginType?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodPost, endpoint, headers)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("set margin type status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var mr marginTypeResp
	if err := json.Unmarshal(body, &mr); err != nil {
		return err
	}
	if mr.Code != 0 && mr.Code != 200 {
		return fmt.Errorf("set margin type failed: %d %s", mr.Code, mr.Msg)
	}
	return nil
}

// SetLeverage 调整杠杆倍数。
func (c *BinanceRESTClient) SetLeverage(symbol string, leverage int) error {
	if c == nil || c.HTTPClient == nil {
		return fmt.Errorf("http client not set")
	}
	params := map[string]string{
		"symbol":   symbol,
		"leverage": strconv.Itoa(leverage),
	}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/leverage?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodPost, endpoint, headers)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("set leverage status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}

// GetBestBidAsk 调用 /fapi/v1/depth 获取最佳买卖价。
func (c *BinanceRESTClient) GetBestBidAsk(symbol string, limit int) (float64, float64, error) {
	if c == nil || c.HTTPClient == nil {
		return 0, 0, fmt.Errorf("http client not set")
	}
	if limit <= 0 {
		limit = 5
	}
	params := url.Values{}
	params.Set("symbol", symbol)
	params.Set("limit", strconv.Itoa(limit))
	endpoint := c.BaseURL + "/fapi/v1/depth?" + params.Encode()
	resp, err := c.sendWithRetry(http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, 0, err
	}
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return 0, 0, fmt.Errorf("get depth status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var dr depthResp
	if err := json.Unmarshal(body, &dr); err != nil {
		return 0, 0, err
	}
	bestBid, bestAsk := parseDepthLevel(dr.Bids), parseDepthLevel(dr.Asks)
	if bestBid == 0 || bestAsk == 0 {
		return 0, 0, fmt.Errorf("empty depth for %s", symbol)
	}
	return bestBid, bestAsk, nil
}

func parseDepthLevel(levels [][]string) float64 {
	if len(levels) == 0 || len(levels[0]) == 0 {
		return 0
	}
	val, err := strconv.ParseFloat(levels[0][0], 64)
	if err != nil {
		return 0
	}
	return val
}

// CancelOrder 调用 /fapi/v1/order 取消订单。
// 支持两种方式：通过orderId（数字）或origClientOrderId（字符串）取消
func (c *BinanceRESTClient) CancelOrder(symbol, orderID string) error {
	if c == nil || c.HTTPClient == nil {
		return fmt.Errorf("http client not set")
	}
	params := map[string]string{
		"symbol": symbol,
	}

	// 判断是否是数字形式的orderId还是字符串形式的clientOrderId
	// 如果以"phoenix-"开头，说明是我们生成的clientOrderId
	if strings.HasPrefix(orderID, "phoenix-") {
		params["origClientOrderId"] = orderID
	} else {
		// 尝试解析为数字，如果成功则用orderId
		if _, err := strconv.ParseInt(orderID, 10, 64); err == nil {
			params["orderId"] = orderID
		} else {
			// 默认作为clientOrderId处理
			params["origClientOrderId"] = orderID
		}
	}

	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/order?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodDelete, endpoint, headers)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("cancel status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}

// CancelOrderByClientID 通过客户端订单ID取消订单
func (c *BinanceRESTClient) CancelOrderByClientID(symbol, clientOrderID string) error {
	if c == nil || c.HTTPClient == nil {
		return fmt.Errorf("http client not set")
	}
	params := map[string]string{
		"symbol":            symbol,
		"origClientOrderId": clientOrderID,
	}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/order?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodDelete, endpoint, headers)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("cancel by client id status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}

// CancelOrderByOrderID 通过交易所订单ID取消订单
func (c *BinanceRESTClient) CancelOrderByOrderID(symbol string, orderID int64) error {
	if c == nil || c.HTTPClient == nil {
		return fmt.Errorf("http client not set")
	}
	params := map[string]string{
		"symbol":  symbol,
		"orderId": strconv.FormatInt(orderID, 10),
	}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/order?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodDelete, endpoint, headers)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("cancel by order id status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}

// CancelAll 调用 /fapi/v1/allOpenOrders 取消指定合约的所有挂单。
func (c *BinanceRESTClient) CancelAll(symbol string) error {
	if c == nil || c.HTTPClient == nil {
		return fmt.Errorf("http client not set")
	}
	if symbol == "" {
		return fmt.Errorf("symbol required")
	}
	params := map[string]string{
		"symbol": symbol,
	}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/allOpenOrders?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodDelete, endpoint, headers)
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("cancel all status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return nil
}

// OpenOrders 查询当前账户的活跃订单列表
func (c *BinanceRESTClient) OpenOrders(symbol string) ([]FuturesOpenOrder, error) {
	if c == nil || c.HTTPClient == nil {
		return nil, fmt.Errorf("http client not set")
	}
	params := map[string]string{}
	if symbol != "" {
		params["symbol"] = strings.ToUpper(symbol)
	}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/openOrders?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodGet, endpoint, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("open orders status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	var raw []struct {
		Symbol        string `json:"symbol"`
		OrderID       int64  `json:"orderId"`
		ClientOrderID string `json:"clientOrderId"`
		Price         string `json:"price"`
		OrigQty       string `json:"origQty"`
		ExecutedQty   string `json:"executedQty"`
		Status        string `json:"status"`
		Side          string `json:"side"`
		Type          string `json:"type"`
		UpdateTime    int64  `json:"updateTime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]FuturesOpenOrder, 0, len(raw))
	for _, r := range raw {
		price, err := strconv.ParseFloat(r.Price, 64)
		if err != nil {
			return nil, fmt.Errorf("parse open order price: %w", err)
		}
		origQty, err := strconv.ParseFloat(r.OrigQty, 64)
		if err != nil {
			return nil, fmt.Errorf("parse open order origQty: %w", err)
		}
		executedQty, err := strconv.ParseFloat(r.ExecutedQty, 64)
		if err != nil {
			return nil, fmt.Errorf("parse open order executedQty: %w", err)
		}
		out = append(out, FuturesOpenOrder{
			Symbol:        r.Symbol,
			Side:          r.Side,
			OrderType:     r.Type,
			Status:        r.Status,
			Price:         price,
			OrigQty:       origQty,
			ExecutedQty:   executedQty,
			OrderID:       r.OrderID,
			ClientOrderID: r.ClientOrderID,
			UpdateTime:    r.UpdateTime,
		})
	}
	return out, nil
}

// NewDefaultHTTPClient 提供一个带超时的 http.Client。
func NewDefaultHTTPClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

// FuturesBalance represents a single asset balance returned by Binance futures.
type FuturesBalance struct {
	Asset     string
	Balance   float64
	Available float64
}

// FuturesPosition represents a single futures position entry.
type FuturesPosition struct {
	Symbol           string
	PositionAmt      float64
	EntryPrice       float64
	MarkPrice        float64
	UnrealizedProfit float64
	MarginType       string
	PositionSide     string
}

// FuturesAccountAsset describes asset-level data.
type FuturesAccountAsset struct {
	Asset            string
	WalletBalance    float64
	AvailableBalance float64
	MarginBalance    float64
	MaxWithdraw      float64
}

// FuturesAccountPosition describes position-level info from /fapi/v2/account.
type FuturesAccountPosition struct {
	Symbol           string
	Leverage         float64
	EntryPrice       float64
	PositionAmt      float64
	UnrealizedProfit float64
	PositionSide     string
}

// FuturesAccount aggregates key account information.
type FuturesAccount struct {
	TotalWalletBalance    float64
	TotalUnrealizedProfit float64
	AvailableBalance      float64
	Assets                []FuturesAccountAsset
	Positions             []FuturesAccountPosition
}

// FuturesOpenOrder 描述 /fapi/v1/openOrders 返回的一条活跃订单
type FuturesOpenOrder struct {
	Symbol        string
	Side          string
	OrderType     string
	Status        string
	Price         float64
	OrigQty       float64
	ExecutedQty   float64
	OrderID       int64
	ClientOrderID string
	UpdateTime    int64
}

// LeverageBracketEntry describes a single leverage bracket.
type LeverageBracketEntry struct {
	Bracket         int
	InitialLeverage float64
	NotionalFloor   float64
	NotionalCap     float64
	MaintMarginRate float64
}

// LeverageBracket contains all brackets for a symbol.
type LeverageBracket struct {
	Symbol   string
	Brackets []LeverageBracketEntry
}

// ExchangeSymbolInfo describes symbol level constraints from /fapi/v1/exchangeInfo.
type ExchangeSymbolInfo struct {
	Symbol            string
	Status            string
	BaseAsset         string
	QuoteAsset        string
	PricePrecision    int
	QuantityPrecision int
	TickSize          float64
	StepSize          float64
	MinNotional       float64
	MaxPrice          float64
	MinPrice          float64
	MaxQty            float64
	MinQty            float64
}

// AccountBalances calls /fapi/v2/balance and returns parsed balances.
func (c *BinanceRESTClient) AccountBalances() ([]FuturesBalance, error) {
	if c == nil || c.HTTPClient == nil {
		return nil, fmt.Errorf("http client not set")
	}
	params := map[string]string{}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v2/balance?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodGet, endpoint, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("balance status %d", resp.StatusCode)
	}
	var raw []struct {
		Asset            string `json:"asset"`
		Balance          string `json:"balance"`
		AvailableBalance string `json:"availableBalance"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]FuturesBalance, 0, len(raw))
	for _, r := range raw {
		bal, err := strconv.ParseFloat(r.Balance, 64)
		if err != nil {
			return nil, fmt.Errorf("parse balance for %s: %w", r.Asset, err)
		}
		avail, err := strconv.ParseFloat(r.AvailableBalance, 64)
		if err != nil {
			return nil, fmt.Errorf("parse available for %s: %w", r.Asset, err)
		}
		out = append(out, FuturesBalance{
			Asset:     r.Asset,
			Balance:   bal,
			Available: avail,
		})
	}
	return out, nil
}

// AccountInfo calls /fapi/v2/account and extracts balances and positions.
func (c *BinanceRESTClient) AccountInfo() (FuturesAccount, error) {
	var result FuturesAccount
	if c == nil || c.HTTPClient == nil {
		return result, fmt.Errorf("http client not set")
	}
	params := map[string]string{}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v2/account?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodGet, endpoint, headers)
	if err != nil {
		return result, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return result, fmt.Errorf("account status %d", resp.StatusCode)
	}
	var raw struct {
		TotalWalletBalance    string `json:"totalWalletBalance"`
		TotalUnrealizedProfit string `json:"totalUnrealizedProfit"`
		AvailableBalance      string `json:"availableBalance"`
		Assets                []struct {
			Asset             string `json:"asset"`
			WalletBalance     string `json:"walletBalance"`
			AvailableBalance  string `json:"availableBalance"`
			MarginBalance     string `json:"marginBalance"`
			MaxWithdrawAmount string `json:"maxWithdrawAmount"`
		} `json:"assets"`
		Positions []struct {
			Symbol           string `json:"symbol"`
			Leverage         string `json:"leverage"`
			EntryPrice       string `json:"entryPrice"`
			PositionAmt      string `json:"positionAmt"`
			UnrealizedProfit string `json:"unrealizedProfit"`
			PositionSide     string `json:"positionSide"`
		} `json:"positions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return result, err
	}
	if result.TotalWalletBalance, err = strconv.ParseFloat(raw.TotalWalletBalance, 64); err != nil {
		return result, fmt.Errorf("parse wallet balance: %w", err)
	}
	if result.TotalUnrealizedProfit, err = strconv.ParseFloat(raw.TotalUnrealizedProfit, 64); err != nil {
		return result, fmt.Errorf("parse unrealized total: %w", err)
	}
	if result.AvailableBalance, err = strconv.ParseFloat(raw.AvailableBalance, 64); err != nil {
		return result, fmt.Errorf("parse available balance: %w", err)
	}
	for _, a := range raw.Assets {
		wallet, err := strconv.ParseFloat(a.WalletBalance, 64)
		if err != nil {
			return result, fmt.Errorf("parse wallet balance for %s: %w", a.Asset, err)
		}
		avail, err := strconv.ParseFloat(a.AvailableBalance, 64)
		if err != nil {
			return result, fmt.Errorf("parse available balance for %s: %w", a.Asset, err)
		}
		marginBal, err := strconv.ParseFloat(a.MarginBalance, 64)
		if err != nil {
			return result, fmt.Errorf("parse margin balance for %s: %w", a.Asset, err)
		}
		maxWithdraw, err := strconv.ParseFloat(a.MaxWithdrawAmount, 64)
		if err != nil {
			return result, fmt.Errorf("parse max withdraw for %s: %w", a.Asset, err)
		}
		result.Assets = append(result.Assets, FuturesAccountAsset{
			Asset:            a.Asset,
			WalletBalance:    wallet,
			AvailableBalance: avail,
			MarginBalance:    marginBal,
			MaxWithdraw:      maxWithdraw,
		})
	}
	for _, p := range raw.Positions {
		lev, err := strconv.ParseFloat(p.Leverage, 64)
		if err != nil {
			return result, fmt.Errorf("parse leverage for %s: %w", p.Symbol, err)
		}
		entry, err := strconv.ParseFloat(p.EntryPrice, 64)
		if err != nil {
			return result, fmt.Errorf("parse entry price for %s: %w", p.Symbol, err)
		}
		posAmt, err := strconv.ParseFloat(p.PositionAmt, 64)
		if err != nil {
			return result, fmt.Errorf("parse position amt for %s: %w", p.Symbol, err)
		}
		unreal, err := strconv.ParseFloat(p.UnrealizedProfit, 64)
		if err != nil {
			return result, fmt.Errorf("parse unrealized for %s: %w", p.Symbol, err)
		}
		result.Positions = append(result.Positions, FuturesAccountPosition{
			Symbol:           p.Symbol,
			Leverage:         lev,
			EntryPrice:       entry,
			PositionAmt:      posAmt,
			UnrealizedProfit: unreal,
			PositionSide:     p.PositionSide,
		})
	}
	return result, nil
}

// PositionRisk calls /fapi/v2/positionRisk and parses positions. Optional symbol filter.
func (c *BinanceRESTClient) PositionRisk(symbol string) ([]FuturesPosition, error) {
	if c == nil || c.HTTPClient == nil {
		return nil, fmt.Errorf("http client not set")
	}
	params := map[string]string{}
	if symbol != "" {
		params["symbol"] = symbol
	}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v2/positionRisk?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodGet, endpoint, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("position status %d", resp.StatusCode)
	}
	var raw []struct {
		Symbol           string `json:"symbol"`
		PositionAmt      string `json:"positionAmt"`
		EntryPrice       string `json:"entryPrice"`
		MarkPrice        string `json:"markPrice"`
		UnrealizedProfit string `json:"unRealizedProfit"`
		MarginType       string `json:"marginType"`
		PositionSide     string `json:"positionSide"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]FuturesPosition, 0, len(raw))
	for _, r := range raw {
		posAmt, err := strconv.ParseFloat(r.PositionAmt, 64)
		if err != nil {
			return nil, fmt.Errorf("parse positionAmt for %s: %w", r.Symbol, err)
		}
		entry, err := strconv.ParseFloat(r.EntryPrice, 64)
		if err != nil {
			return nil, fmt.Errorf("parse entryPrice for %s: %w", r.Symbol, err)
		}
		mark, err := strconv.ParseFloat(r.MarkPrice, 64)
		if err != nil {
			return nil, fmt.Errorf("parse markPrice for %s: %w", r.Symbol, err)
		}
		unreal, err := strconv.ParseFloat(r.UnrealizedProfit, 64)
		if err != nil {
			return nil, fmt.Errorf("parse unrealized for %s: %w", r.Symbol, err)
		}
		out = append(out, FuturesPosition{
			Symbol:           r.Symbol,
			PositionAmt:      posAmt,
			EntryPrice:       entry,
			MarkPrice:        mark,
			UnrealizedProfit: unreal,
			MarginType:       r.MarginType,
			PositionSide:     r.PositionSide,
		})
	}
	return out, nil
}

// LeverageBrackets calls /fapi/v1/leverageBracket and parses symbol brackets.
func (c *BinanceRESTClient) LeverageBrackets(symbol string) ([]LeverageBracket, error) {
	if c == nil || c.HTTPClient == nil {
		return nil, fmt.Errorf("http client not set")
	}
	params := map[string]string{}
	if symbol != "" {
		params["symbol"] = symbol
	}
	c.applyRecvWindow(params)
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/leverageBracket?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodGet, endpoint, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("leverage bracket status %d", resp.StatusCode)
	}
	var raw []struct {
		Symbol   string `json:"symbol"`
		Brackets []struct {
			Bracket         int     `json:"bracket"`
			InitialLeverage float64 `json:"initialLeverage"`
			NotionalCap     float64 `json:"notionalCap"`
			NotionalFloor   float64 `json:"notionalFloor"`
			MaintMarginRate float64 `json:"maintMarginRatio"`
		} `json:"brackets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]LeverageBracket, 0, len(raw))
	for _, r := range raw {
		lb := LeverageBracket{Symbol: r.Symbol}
		for _, b := range r.Brackets {
			lb.Brackets = append(lb.Brackets, LeverageBracketEntry{
				Bracket:         b.Bracket,
				InitialLeverage: b.InitialLeverage,
				NotionalFloor:   b.NotionalFloor,
				NotionalCap:     b.NotionalCap,
				MaintMarginRate: b.MaintMarginRate,
			})
		}
		out = append(out, lb)
	}
	return out, nil
}

// ExchangeInfo fetches /fapi/v1/exchangeInfo and extracts key symbol constraints.
func (c *BinanceRESTClient) ExchangeInfo(symbol string) ([]ExchangeSymbolInfo, error) {
	if c == nil || c.HTTPClient == nil {
		return nil, fmt.Errorf("http client not set")
	}
	params := map[string]string{}
	if symbol != "" {
		params["symbol"] = symbol
	}
	query, sig := SignParams(params, c.Secret)
	endpoint := c.BaseURL + "/fapi/v1/exchangeInfo?" + query + "&signature=" + url.QueryEscape(sig)
	headers := map[string]string{"X-MBX-APIKEY": c.APIKey}
	resp, err := c.sendWithRetry(http.MethodGet, endpoint, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("exchangeInfo status %d", resp.StatusCode)
	}
	var raw struct {
		Symbols []struct {
			Symbol            string `json:"symbol"`
			Status            string `json:"status"`
			BaseAsset         string `json:"baseAsset"`
			QuoteAsset        string `json:"quoteAsset"`
			PricePrecision    int    `json:"pricePrecision"`
			QuantityPrecision int    `json:"quantityPrecision"`
			Filters           []struct {
				FilterType  string `json:"filterType"`
				MinPrice    string `json:"minPrice"`
				MaxPrice    string `json:"maxPrice"`
				TickSize    string `json:"tickSize"`
				StepSize    string `json:"stepSize"`
				MinQty      string `json:"minQty"`
				MaxQty      string `json:"maxQty"`
				Notional    string `json:"notional"`
				MinNotional string `json:"minNotional"`
			} `json:"filters"`
		} `json:"symbols"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]ExchangeSymbolInfo, 0, len(raw.Symbols))
	for _, s := range raw.Symbols {
		info := ExchangeSymbolInfo{
			Symbol:            s.Symbol,
			Status:            s.Status,
			BaseAsset:         s.BaseAsset,
			QuoteAsset:        s.QuoteAsset,
			PricePrecision:    s.PricePrecision,
			QuantityPrecision: s.QuantityPrecision,
		}
		for _, f := range s.Filters {
			switch f.FilterType {
			case "PRICE_FILTER":
				info.TickSize, _ = strconv.ParseFloat(f.TickSize, 64)
				info.MinPrice, _ = strconv.ParseFloat(f.MinPrice, 64)
				info.MaxPrice, _ = strconv.ParseFloat(f.MaxPrice, 64)
			case "LOT_SIZE":
				info.StepSize, _ = strconv.ParseFloat(f.StepSize, 64)
				info.MinQty, _ = strconv.ParseFloat(f.MinQty, 64)
				info.MaxQty, _ = strconv.ParseFloat(f.MaxQty, 64)
			case "MIN_NOTIONAL":
				val := f.MinNotional
				if val == "" {
					val = f.Notional
				}
				info.MinNotional, _ = strconv.ParseFloat(val, 64)
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func (c *BinanceRESTClient) applyRecvWindow(params map[string]string) {
	if c != nil && c.RecvWindowMs > 0 {
		params["recvWindow"] = fmt.Sprintf("%d", c.RecvWindowMs)
	}
}

func (c *BinanceRESTClient) waitLimit() {
	if c != nil && c.Limiter != nil {
		c.Limiter.Wait()
	}
}

func (c *BinanceRESTClient) sendWithRetry(method, endpoint string, headers map[string]string) (*http.Response, error) {
	maxAttempts := c.MaxRetries
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	delay := c.RetryDelay
	if delay <= 0 {
		delay = 200 * time.Millisecond
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		req, _ := http.NewRequest(method, endpoint, bytes.NewBuffer(nil))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		c.waitLimit()
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
		} else {
			if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 418 {
				lastErr = fmt.Errorf("status %d", resp.StatusCode)
				resp.Body.Close()
			} else {
				return resp, nil
			}
		}
		time.Sleep(delay * time.Duration(attempt+1))
	}
	return nil, fmt.Errorf("request failed after %d attempts: %w", maxAttempts, lastErr)
}

func validateTimeInForce(tif string, postOnly bool) error {
	if postOnly {
		return nil
	}
	switch strings.ToUpper(tif) {
	case "GTC", "IOC", "FOK":
		return nil
	case "":
		return errors.New("timeInForce required when not postOnly")
	default:
		return fmt.Errorf("unsupported timeInForce %s", tif)
	}
}
