package browser

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// scriptedCDP records calls and answers them from a per-method table.
type scriptedCDP struct {
	mu      sync.Mutex
	calls   []recordedCall
	answers map[string]func(params map[string]any) string
}

func (s *scriptedCDP) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	b, _ := json.Marshal(params)
	var p map[string]any
	json.Unmarshal(b, &p)
	s.mu.Lock()
	s.calls = append(s.calls, recordedCall{method, p})
	ans := s.answers[method]
	s.mu.Unlock()
	if ans == nil {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(ans(p)), nil
}

func (s *scriptedCDP) take() []recordedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.calls
	s.calls = nil
	return out
}

// newTextTab returns a tab whose tapped node is editable iff editable, and
// whose execCommand insertion reports execOK.
func newTextTab(editable, execOK bool) (*UserTab, *scriptedCDP) {
	s := &scriptedCDP{answers: map[string]func(map[string]any) string{
		"DOM.getNodeForLocation": func(map[string]any) string { return `{"backendNodeId":42,"frameId":"F"}` },
		"DOM.resolveNode":        func(map[string]any) string { return `{"object":{"type":"object","objectId":"obj-1"}}` },
		"Runtime.callFunctionOn": func(p map[string]any) string {
			ok := editable
			if p["functionDeclaration"] == insertTextFn {
				ok = execOK
			}
			if ok {
				return `{"result":{"type":"boolean","value":true}}`
			}
			return `{"result":{"type":"boolean","value":false}}`
		},
	}}
	tab := newFakeTab(func(ctx context.Context, expr string) (json.RawMessage, error) {
		return json.RawMessage(`{"result":{"value":{"secure":false,"editable":true}}}`), nil
	})
	tab.callFn = s.call
	return tab, s
}

func TestTapOnEditableThenTextUsesExecCommand(t *testing.T) {
	tab, s := newTextTab(true, true)
	ctx := context.Background()
	if err := tab.HandleInput(ctx, InputMessage{Type: "tap", X: 195.4, Y: 271.6}); err != nil {
		t.Fatal(err)
	}
	calls := s.take()
	want := []string{"Input.dispatchMouseEvent", "Input.dispatchMouseEvent", "DOM.getNodeForLocation", "DOM.resolveNode", "Runtime.callFunctionOn", "Runtime.releaseObjectGroup"}
	if got := methods(calls); !reflect.DeepEqual(got, want) {
		t.Fatalf("tap calls = %v, want %v", got, want)
	}
	if loc := calls[2].params; loc["x"] != float64(195) || loc["y"] != float64(272) {
		t.Fatalf("getNodeForLocation params = %v", loc)
	}
	if calls[3].params["backendNodeId"] != float64(42) || calls[4].params["objectId"] != "obj-1" || calls[4].params["functionDeclaration"] != tapFocusFn {
		t.Fatalf("unexpected resolve/call params %v %v", calls[3].params, calls[4].params)
	}
	if tab.getEditNode() != 42 {
		t.Fatalf("edit node = %d", tab.getEditNode())
	}

	if err := tab.HandleInput(ctx, InputMessage{Type: "text", Text: "t"}); err != nil {
		t.Fatal(err)
	}
	calls = s.take()
	want = []string{"DOM.resolveNode", "Runtime.callFunctionOn", "Runtime.releaseObjectGroup"}
	if got := methods(calls); !reflect.DeepEqual(got, want) {
		t.Fatalf("text calls = %v, want %v (no Input.insertText)", got, want)
	}
	if calls[1].params["functionDeclaration"] != insertTextFn {
		t.Fatal("text must use insertTextFn")
	}
	args, _ := calls[1].params["arguments"].([]any)
	if len(args) != 1 || args[0].(map[string]any)["value"] != "t" {
		t.Fatalf("arguments = %v", calls[1].params["arguments"])
	}
}

func TestTextWithoutTappedFieldUsesInsertText(t *testing.T) {
	tab, s := newTextTab(true, true)
	if err := tab.HandleInput(context.Background(), InputMessage{Type: "text", Text: "abc"}); err != nil {
		t.Fatal(err)
	}
	calls := s.take()
	if got := methods(calls); !reflect.DeepEqual(got, []string{"Input.insertText"}) {
		t.Fatalf("calls = %v", got)
	}
	if calls[0].params["text"] != "abc" {
		t.Fatalf("params = %v", calls[0].params)
	}
}

func TestTapOnNonEditableClearsTarget(t *testing.T) {
	tab, s := newTextTab(false, true)
	tab.setEditNode(7)
	if err := tab.HandleInput(context.Background(), InputMessage{Type: "tap", X: 1, Y: 1}); err != nil {
		t.Fatal(err)
	}
	if tab.getEditNode() != 0 {
		t.Fatalf("edit node = %d, want 0", tab.getEditNode())
	}
	s.take()
	tab.HandleInput(context.Background(), InputMessage{Type: "text", Text: "x"})
	if got := methods(s.take()); !reflect.DeepEqual(got, []string{"Input.insertText"}) {
		t.Fatalf("calls = %v", got)
	}
}

func TestExecCommandFailureFallsBackToInsertText(t *testing.T) {
	tab, s := newTextTab(true, false)
	ctx := context.Background()
	tab.HandleInput(ctx, InputMessage{Type: "tap", X: 1, Y: 1})
	s.take()
	if err := tab.HandleInput(ctx, InputMessage{Type: "text", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"DOM.resolveNode", "Runtime.callFunctionOn", "Runtime.releaseObjectGroup", "Input.insertText"}
	if got := methods(s.take()); !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
}

func TestMainFrameNavigationClearsTextTarget(t *testing.T) {
	tab, _ := newTextTab(true, true)
	tab.setEditNode(42)
	tab.onEvent("Page.frameNavigated", json.RawMessage(`{"frame":{"url":"https://example.com/"}}`))
	if tab.getEditNode() != 0 {
		t.Fatal("navigation must clear the text target")
	}
}

func TestTextInputScriptsNeverReadValue(t *testing.T) {
	for name, fn := range map[string]string{"tapFocusFn": tapFocusFn, "insertTextFn": insertTextFn} {
		if strings.Contains(fn, ".value") {
			t.Errorf("%s must not read field values", name)
		}
	}
	if !strings.Contains(insertTextFn, "execCommand('insertText'") {
		t.Error("insertTextFn must insert via execCommand")
	}
}
