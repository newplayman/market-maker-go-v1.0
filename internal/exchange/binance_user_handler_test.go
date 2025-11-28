package gateway

import "testing"

func TestBinanceUserHandler(t *testing.T) {
	var gotOrder OrderUpdate
	var gotAccount AccountUpdate
	h := &BinanceUserHandler{
		OnOrderUpdate:   func(o OrderUpdate) { gotOrder = o },
		OnAccountUpdate: func(a AccountUpdate) { gotAccount = a },
	}
	recordOrder := []byte(`{"stream":"lk","data":{"e":"ORDER_TRADE_UPDATE","o":{"s":"ETHUSDC","S":"BUY","o":"LIMIT","X":"NEW","x":"NEW","i":1,"c":"cid","p":"100.0","q":"1","l":"0","z":"0","L":"0","rp":"0","N":"USDC","n":"0","ps":"BOTH"}}}`)
	h.OnRawMessage(recordOrder)
	if gotOrder.Symbol != "ETHUSDC" || gotOrder.ClientOrderID != "cid" {
		t.Fatalf("order callback not triggered: %+v", gotOrder)
	}
	recordAccount := []byte(`{"stream":"lk","data":{"e":"ACCOUNT_UPDATE","a":{"m":"ORDER","B":[{"a":"USDC","wb":"1","cw":"1"}]}}}`)
	h.OnRawMessage(recordAccount)
	if gotAccount.Reason != "ORDER" || len(gotAccount.Balances) != 1 {
		t.Fatalf("account callback not triggered: %+v", gotAccount)
	}
}
