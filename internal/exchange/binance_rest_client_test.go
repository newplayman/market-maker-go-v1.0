package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBinanceRESTClientPlaceCancel(t *testing.T) {
	timeNowMillis = func() int64 { return 1234567890000 } // deterministic
	defer func() { timeNowMillis = func() int64 { return time.Now().UnixMilli() } }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if !strings.Contains(r.URL.RawQuery, "signature=") {
				t.Fatalf("missing signature")
			}
			io.WriteString(w, `{"orderId":"1001"}`)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(200)
			return
		}
		t.Fatalf("unexpected method %s", r.Method)
	}))
	defer ts.Close()

	cli := &BinanceRESTClient{
		BaseURL:    ts.URL,
		APIKey:     "key",
		Secret:     "secret",
		HTTPClient: ts.Client(),
		Limiter:    &mockLimiter{},
	}
	id, err := cli.PlaceLimit("BTCUSDT", "BUY", "GTC", 100, 1, false, true, "cid")
	if err != nil {
		t.Fatalf("place err: %v", err)
	}
	if id != "1001" {
		t.Fatalf("unexpected order id %s", id)
	}
	if err := cli.CancelOrder("BTCUSDT", id); err != nil {
		t.Fatalf("cancel err: %v", err)
	}
}

func TestBinanceRESTClientAccountBalances(t *testing.T) {
	timeNowMillis = func() int64 { return 1234567890000 }
	defer func() { timeNowMillis = func() int64 { return time.Now().UnixMilli() } }()

	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		gotQuery = r.URL.RawQuery
		io.WriteString(w, `[{"asset":"USDC","balance":"100.50","availableBalance":"80.25"}]`)
	}))
	defer ts.Close()

	cli := &BinanceRESTClient{
		BaseURL:      ts.URL,
		APIKey:       "key",
		Secret:       "secret",
		HTTPClient:   ts.Client(),
		RecvWindowMs: 7000,
		Limiter:      &mockLimiter{},
	}
	bals, err := cli.AccountBalances()
	if err != nil {
		t.Fatalf("balance err: %v", err)
	}
	if len(bals) != 1 {
		t.Fatalf("expected 1 balance item, got %d", len(bals))
	}
	if bals[0].Asset != "USDC" || bals[0].Balance != 100.50 || bals[0].Available != 80.25 {
		t.Fatalf("unexpected balance %+v", bals[0])
	}
	if !strings.Contains(gotQuery, "recvWindow=7000") {
		t.Fatalf("recvWindow not applied: %s", gotQuery)
	}
}

func TestBinanceRESTClientPositionRisk(t *testing.T) {
	timeNowMillis = func() int64 { return 1234567890000 }
	defer func() { timeNowMillis = func() int64 { return time.Now().UnixMilli() } }()

	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		gotQuery = r.URL.RawQuery
		io.WriteString(w, `[{"symbol":"ETHUSDC","positionAmt":"0.500","entryPrice":"3200.0","markPrice":"3210.0","unRealizedProfit":"5.0","marginType":"isolated","positionSide":"BOTH"}]`)
	}))
	defer ts.Close()

	cli := &BinanceRESTClient{
		BaseURL:      ts.URL,
		APIKey:       "key",
		Secret:       "secret",
		HTTPClient:   ts.Client(),
		RecvWindowMs: 6000,
		Limiter:      &mockLimiter{},
	}
	pos, err := cli.PositionRisk("ETHUSDC")
	if err != nil {
		t.Fatalf("position err: %v", err)
	}
	if len(pos) != 1 {
		t.Fatalf("expected 1 position, got %d", len(pos))
	}
	p := pos[0]
	if p.Symbol != "ETHUSDC" || p.PositionAmt != 0.5 || p.EntryPrice != 3200 || p.MarkPrice != 3210 || p.UnrealizedProfit != 5 {
		t.Fatalf("unexpected position %+v", p)
	}
	if p.MarginType != "isolated" || p.PositionSide != "BOTH" {
		t.Fatalf("unexpected margin info %+v", p)
	}
	if !strings.Contains(gotQuery, "symbol=ETHUSDC") || !strings.Contains(gotQuery, "recvWindow=6000") {
		t.Fatalf("missing query params: %s", gotQuery)
	}
}

func TestBinanceRESTClientAccountInfo(t *testing.T) {
	timeNowMillis = func() int64 { return 1234567890000 }
	defer func() { timeNowMillis = func() int64 { return time.Now().UnixMilli() } }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		io.WriteString(w, `{
  "totalWalletBalance":"100.5",
  "totalUnrealizedProfit":"1.2",
  "availableBalance":"95.0",
  "assets":[{"asset":"USDC","walletBalance":"100.5","availableBalance":"95.0","marginBalance":"100.5","maxWithdrawAmount":"90.0"}],
  "positions":[{"symbol":"ETHUSDC","leverage":"20","entryPrice":"3200","positionAmt":"0.5","unrealizedProfit":"5.0","positionSide":"BOTH"}]
}`)
	}))
	defer ts.Close()

	cli := &BinanceRESTClient{
		BaseURL:      ts.URL,
		APIKey:       "key",
		Secret:       "secret",
		HTTPClient:   ts.Client(),
		RecvWindowMs: 6000,
		Limiter:      &mockLimiter{},
	}
	info, err := cli.AccountInfo()
	if err != nil {
		t.Fatalf("account err: %v", err)
	}
	if info.TotalWalletBalance != 100.5 || info.TotalUnrealizedProfit != 1.2 || info.AvailableBalance != 95 {
		t.Fatalf("unexpected totals %+v", info)
	}
	if len(info.Assets) != 1 || info.Assets[0].Asset != "USDC" || info.Assets[0].MaxWithdraw != 90 {
		t.Fatalf("unexpected assets %+v", info.Assets)
	}
	if len(info.Positions) != 1 || info.Positions[0].Leverage != 20 || info.Positions[0].PositionAmt != 0.5 {
		t.Fatalf("unexpected positions %+v", info.Positions)
	}
}

func TestBinanceRESTClientLeverageBrackets(t *testing.T) {
	timeNowMillis = func() int64 { return 1234567890000 }
	defer func() { timeNowMillis = func() int64 { return time.Now().UnixMilli() } }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `[{"symbol":"ETHUSDC","brackets":[{"bracket":1,"initialLeverage":50,"notionalCap":10000,"notionalFloor":0,"maintMarginRatio":0.01},{"bracket":2,"initialLeverage":25,"notionalCap":50000,"notionalFloor":10000,"maintMarginRatio":0.02}]}]`)
	}))
	defer ts.Close()

	cli := &BinanceRESTClient{
		BaseURL:      ts.URL,
		APIKey:       "key",
		Secret:       "secret",
		HTTPClient:   ts.Client(),
		RecvWindowMs: 5000,
		Limiter:      &mockLimiter{},
	}
	brackets, err := cli.LeverageBrackets("ETHUSDC")
	if err != nil {
		t.Fatalf("bracket err: %v", err)
	}
	if len(brackets) != 1 || brackets[0].Symbol != "ETHUSDC" {
		t.Fatalf("unexpected brackets %+v", brackets)
	}
	if len(brackets[0].Brackets) != 2 || brackets[0].Brackets[0].InitialLeverage != 50 || brackets[0].Brackets[1].NotionalFloor != 10000 {
		t.Fatalf("unexpected bracket entries %+v", brackets[0].Brackets)
	}
}

func TestBinanceRESTClientExchangeInfo(t *testing.T) {
	timeNowMillis = func() int64 { return 1234567890000 }
	defer func() { timeNowMillis = func() int64 { return time.Now().UnixMilli() } }()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{
  "symbols": [
    {
      "symbol": "ETHUSDC",
      "status": "TRADING",
      "baseAsset": "ETH",
      "quoteAsset": "USDC",
      "pricePrecision": 2,
      "quantityPrecision": 3,
      "filters": [
        {"filterType":"PRICE_FILTER","minPrice":"0.10","maxPrice":"100000","tickSize":"0.10"},
        {"filterType":"LOT_SIZE","minQty":"0.001","maxQty":"1000","stepSize":"0.001"},
        {"filterType":"MIN_NOTIONAL","minNotional":"5"}
      ]
    }
  ]
}`)
	}))
	defer ts.Close()

	cli := &BinanceRESTClient{
		BaseURL:      ts.URL,
		APIKey:       "key",
		Secret:       "secret",
		HTTPClient:   ts.Client(),
		RecvWindowMs: 5000,
		Limiter:      &mockLimiter{},
	}
	info, err := cli.ExchangeInfo("ETHUSDC")
	if err != nil {
		t.Fatalf("exchange info err: %v", err)
	}
	if len(info) != 1 {
		t.Fatalf("expected 1 symbol, got %d", len(info))
	}
	s := info[0]
	if s.Symbol != "ETHUSDC" || s.TickSize != 0.10 || s.StepSize != 0.001 || s.MinNotional != 5 {
		t.Fatalf("unexpected symbol info %+v", s)
	}
	if s.PricePrecision != 2 || s.QuantityPrecision != 3 {
		t.Fatalf("unexpected precision %+v", s)
	}
}

func TestBinanceRESTClientGetBestBidAsk(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fapi/v1/depth" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("symbol") != "ETHUSDC" {
			t.Fatalf("unexpected symbol query %s", r.URL.RawQuery)
		}
		if r.URL.Query().Get("limit") != "10" {
			t.Fatalf("unexpected limit %s", r.URL.Query().Get("limit"))
		}
		io.WriteString(w, `{"lastUpdateId":1,"bids":[["100.5","1"]],"asks":[["101.7","2"]]}`)
	}))
	defer ts.Close()

	cli := &BinanceRESTClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		Limiter:    &mockLimiter{},
	}
	bid, ask, err := cli.GetBestBidAsk("ETHUSDC", 10)
	if err != nil {
		t.Fatalf("get depth err: %v", err)
	}
	if bid != 100.5 || ask != 101.7 {
		t.Fatalf("unexpected best bid/ask %.2f %.2f", bid, ask)
	}
}

func TestBinanceRESTClientGetBestBidAskError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "oops", http.StatusBadRequest)
	}))
	defer ts.Close()
	cli := &BinanceRESTClient{
		BaseURL:    ts.URL,
		HTTPClient: ts.Client(),
		Limiter:    &mockLimiter{},
	}
	if _, _, err := cli.GetBestBidAsk("ETHUSDC", 5); err == nil {
		t.Fatalf("expected error")
	}
}

type mockLimiter struct {
	called int
}

func (m *mockLimiter) Wait() {
	m.called++
}
