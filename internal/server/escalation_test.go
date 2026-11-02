package server

import "testing"

func TestEscalationStoreLoginURL(t *testing.T) {
	es := NewEscalationStore()
	es.CreateWithLoginURL("e1", "s1", "expired", 10, "https://example.com/login")
	got := es.GetBySession("s1")
	if got == nil || got.LoginURL != "https://example.com/login" {
		t.Fatalf("got %+v", got)
	}
	es.Create("e2", "s2", "q", 10)
	if g := es.GetBySession("s2"); g == nil || g.LoginURL != "" {
		t.Fatalf("got %+v", g)
	}
}
