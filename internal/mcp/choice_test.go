package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/experimental"
)

func TestOfferChoicesGatedByChoiceCards(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := NewServerForHTTP("sess", "http://127.0.0.1:0")
	if listToolNames(t)["offer_choices"] {
		t.Error("offer_choices must be hidden while choice_cards is off")
	}
	if _, err := s.callTool("offer_choices", json.RawMessage(`{}`)); err == nil {
		t.Error("offer_choices call must be rejected while choice_cards is off")
	}
	if err := experimental.Set("choice_cards", true); err != nil {
		t.Fatal(err)
	}
	if !listToolNames(t)["offer_choices"] {
		t.Error("offer_choices must be visible while choice_cards is on")
	}
}

func TestOfferChoicesReturnsImmediately(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := experimental.Set("choice_cards", true); err != nil {
		t.Fatal(err)
	}
	var gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.URL.Path, string(b)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer ts.Close()
	s := NewServerForHTTP("sess", ts.URL)

	start := time.Now()
	res, rpcErr := s.callTool("offer_choices", json.RawMessage(`{"prompt":"どうする？","choices":[{"label":"A","message":"Aで"},{"label":"B"}]}`))
	if rpcErr != nil {
		t.Fatalf("rpc error: %v", rpcErr)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("offer_choices must not block")
	}
	if gotPath != "/api/sessions/sess/choice-cards" || !strings.Contains(gotBody, `"Aで"`) {
		t.Errorf("request = %s %s", gotPath, gotBody)
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), "ターンを終えて") {
		t.Errorf("result should say the agent need not wait: %s", b)
	}

	// 1 or 5 choices are rejected before any request.
	for _, args := range []string{
		`{"prompt":"q","choices":[{"label":"A"}]}`,
		`{"prompt":"q","choices":[{"label":"1"},{"label":"2"},{"label":"3"},{"label":"4"},{"label":"5"}]}`,
	} {
		if _, rpcErr := s.callTool("offer_choices", json.RawMessage(args)); rpcErr == nil {
			t.Errorf("expected error for %s", args)
		}
	}
}
