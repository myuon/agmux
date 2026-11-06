package browser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestParseFocusResult(t *testing.T) {
	st, err := parseFocusResult(json.RawMessage(`{"result":{"type":"object","value":{"secure":true,"editable":true}}}`))
	if err != nil || !st.Secure || !st.Editable {
		t.Fatalf("got %+v %v", st, err)
	}
	if _, err := parseFocusResult(json.RawMessage(`{"result":{},"exceptionDetails":{"text":"x"}}`)); err == nil {
		t.Fatal("expected error on exception")
	}
	if _, err := parseFocusResult(json.RawMessage(`{"result":{"type":"undefined"}}`)); err == nil {
		t.Fatal("expected error on missing value")
	}
	if _, err := parseFocusResult(json.RawMessage(`nope`)); err == nil {
		t.Fatal("expected error on bad json")
	}
}

func TestFocusExprNeverReadsValue(t *testing.T) {
	if strings.Contains(focusExpr, ".value") {
		t.Fatal("focus expression must not read field values")
	}
	for _, want := range []string{"password", "current-password", "new-password", "one-time-code", "contentDocument", "shadowRoot"} {
		if !strings.Contains(focusExpr, want) {
			t.Errorf("focusExpr missing %q", want)
		}
	}
}

func newFakeTab(eval func(ctx context.Context, expr string) (json.RawMessage, error)) *UserTab {
	return &UserTab{subs: map[*Subscription]struct{}{}, evalFn: eval}
}

func TestScheduleFocusCheckBroadcasts(t *testing.T) {
	var gotExpr string
	tab := newFakeTab(func(ctx context.Context, expr string) (json.RawMessage, error) {
		gotExpr = expr
		return json.RawMessage(`{"result":{"value":{"secure":true,"editable":true}}}`), nil
	})
	s := &Subscription{ch: make(chan []byte, 8), tab: tab}
	s.C = s.ch
	tab.subs[s] = struct{}{}

	tab.scheduleFocusCheck()
	select {
	case msg := <-s.C:
		var m map[string]any
		if err := json.Unmarshal(msg, &m); err != nil {
			t.Fatal(err)
		}
		if m["type"] != "focus" || m["secure"] != true || m["editable"] != true || len(m) != 3 {
			t.Fatalf("unexpected message %s", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no focus message")
	}
	if gotExpr != focusExpr {
		t.Fatal("unexpected expression")
	}
	tab.mu.Lock()
	cached := tab.lastFocus != nil
	tab.mu.Unlock()
	if !cached {
		t.Fatal("lastFocus not cached")
	}
}

func TestScheduleFocusCheckErrorSendsNothing(t *testing.T) {
	tab := newFakeTab(func(ctx context.Context, expr string) (json.RawMessage, error) {
		return nil, context.DeadlineExceeded
	})
	s := &Subscription{ch: make(chan []byte, 8), tab: tab}
	s.C = s.ch
	tab.subs[s] = struct{}{}
	tab.scheduleFocusCheck()
	select {
	case msg := <-s.C:
		t.Fatalf("unexpected message %s", msg)
	case <-time.After(focusCheckDelay + 300*time.Millisecond):
	}
}

func TestFocusMessageSurvivesFullQueue(t *testing.T) {
	tab := newFakeTab(func(ctx context.Context, expr string) (json.RawMessage, error) {
		return json.RawMessage(`{"result":{"value":{"secure":true,"editable":true}}}`), nil
	})
	s := &Subscription{ch: make(chan []byte, 2), tab: tab}
	s.C = s.ch
	tab.subs[s] = struct{}{}
	s.ch <- []byte(`{"type":"frame"}`)
	s.ch <- []byte(`{"type":"frame"}`)

	tab.scheduleFocusCheck()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case msg := <-s.C:
			if strings.Contains(string(msg), `"focus"`) {
				return
			}
		case <-deadline:
			t.Fatal("focus message dropped by a full queue")
		}
	}
}
