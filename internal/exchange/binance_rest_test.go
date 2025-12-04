package gateway

import "testing"

func TestBinanceRESTStubPlaceCancel(t *testing.T) {
	cli := &BinanceRESTStub{}
	id, err := cli.PlaceLimit("BTCUSDT", "BUY", "GTC", 100, 1, false, true, "cid")
	if err != nil {
		t.Fatalf("place error: %v", err)
	}
	if id == "" {
		t.Fatalf("expected order id")
	}
	if err := cli.CancelOrder("BTCUSDT", id); err != nil {
		t.Fatalf("cancel error: %v", err)
	}
	if mid, err := cli.PlaceMarket("BTCUSDT", "SELL", 2, true, "cid2"); err != nil || mid == "" {
		t.Fatalf("place market error: %v id=%s", err, mid)
	}
}
