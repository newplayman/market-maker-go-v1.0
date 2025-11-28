package gateway

import "testing"

func TestParseCombinedDepth(t *testing.T) {
	raw := []byte(`{
		"stream":"btcusdt@depth20@100ms",
		"data":{
		  "s":"BTCUSDT",
		  "b":[["100.1","1.2"],["100.0","2"]],
		  "a":[["100.2","1.1"],["100.3","2.2"]]
		}
	}`)
	sym, bid, ask, err := ParseCombinedDepth(raw)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if sym != "BTCUSDT" || bid != 100.1 || ask != 100.2 {
		t.Fatalf("unexpected parse result: %s %.3f %.3f", sym, bid, ask)
	}
}

func TestParseUserOrderUpdate(t *testing.T) {
	raw := []byte(`{
		"stream":"listenKey",
		"data":{
			"e":"ORDER_TRADE_UPDATE",
			"o":{
				"s":"ETHUSDC","S":"BUY","o":"LIMIT","X":"NEW","x":"NEW",
				"i":1001,"c":"cid","p":"2700.10","q":"1.5","l":"0.2","z":"0.2",
				"L":"2700.00","rp":"0","N":"USDC","n":"0","ps":"BOTH"
			}
		}
	}`)
	ev, err := ParseUserData(raw)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if ev.EventType != "ORDER_TRADE_UPDATE" || ev.Order == nil {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if ev.Order.Price != 2700.10 || ev.Order.OrigQty != 1.5 || ev.Order.LastFilledQty != 0.2 {
		t.Fatalf("unexpected order payload: %+v", ev.Order)
	}
}

func TestParseUserAccountUpdate(t *testing.T) {
	raw := []byte(`{
		"stream":"listenKey",
		"data":{
			"e":"ACCOUNT_UPDATE",
			"a":{
				"m":"ORDER",
				"B":[{"a":"USDC","wb":"100.5","cw":"80.2"}],
				"P":[{"s":"ETHUSDC","pa":"0.1","ep":"2500","cr":"5","mt":"isolated","ps":"BOTH"}]
			}
		}
	}`)
	ev, err := ParseUserData(raw)
	if err != nil {
		t.Fatalf("parse err: %v", err)
	}
	if ev.EventType != "ACCOUNT_UPDATE" || ev.Account == nil {
		t.Fatalf("unexpected event: %+v", ev)
	}
	if len(ev.Account.Balances) != 1 || ev.Account.Balances[0].WalletBalance != 100.5 {
		t.Fatalf("unexpected balances: %+v", ev.Account.Balances)
	}
	if len(ev.Account.Positions) != 1 || ev.Account.Positions[0].PositionAmt != 0.1 {
		t.Fatalf("unexpected positions: %+v", ev.Account.Positions)
	}
}
