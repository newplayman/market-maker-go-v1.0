package gateway

import "testing"

type stubREST struct {
	placed   bool
	canceled bool
	err      error
}

func (s *stubREST) Place(symbol, side string, price, qty float64) (string, error) {
	s.placed = true
	return "id-1", s.err
}
func (s *stubREST) Cancel(orderID string) error {
	s.canceled = true
	return s.err
}

func TestClientPlaceCancel(t *testing.T) {
	r := &stubREST{}
	c := NewClient(r)
	if _, err := c.Place("BTCUSDT", "BUY", 100, 1); err != nil {
		t.Fatalf("place err: %v", err)
	}
	if err := c.Cancel("id-1"); err != nil {
		t.Fatalf("cancel err: %v", err)
	}
	if !r.placed || !r.canceled {
		t.Fatalf("expected place and cancel to be called")
	}
}
