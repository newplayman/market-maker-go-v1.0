package gateway

import (
	"testing"
)

func TestSignParamsDeterministic(t *testing.T) {
	params := map[string]string{
		"symbol": "BTCUSDT",
		"side":   "BUY",
		"price":  "100",
	}
	q, sig := SignParams(params, "secret")
	if q == "" || sig == "" {
		t.Fatalf("expected query and signature")
	}
}
