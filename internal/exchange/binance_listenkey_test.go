package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListenKeyLifecycle(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Write([]byte(`{"listenKey":"lk-1"}`))
		case http.MethodPut:
			w.WriteHeader(200)
		case http.MethodDelete:
			w.WriteHeader(200)
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer ts.Close()

	c := &ListenKeyClient{BaseURL: ts.URL, APIKey: "key", HTTPClient: ts.Client()}
	lk, err := c.NewListenKey()
	if err != nil || lk != "lk-1" {
		t.Fatalf("create listenKey: %v %s", err, lk)
	}
	if err := c.KeepAlive(lk); err != nil {
		t.Fatalf("keepalive err: %v", err)
	}
	if err := c.CloseListenKey(lk); err != nil {
		t.Fatalf("close err: %v", err)
	}
}
