package browser

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// FocusState describes the element focused in the user tab. It is sent to the
// client so the on-screen keyboard can switch to password mode (mobile
// keyboards neither learn from nor suggest for type="password" fields).
//
// Only booleans are ever produced: the field's value is never read.
type FocusState struct {
	// Secure is true for input[type=password] and for autocomplete tokens
	// current-password / new-password / one-time-code.
	Secure bool `json:"secure"`
	// Editable is true when the focused element accepts text.
	Editable bool `json:"editable"`
}

type focusMessage struct {
	Type string `json:"type"` // "focus"
	FocusState
}

// focusCheckDelay lets the page process the click/keypress (focus moves and
// navigation settle) before the focused element is inspected.
const focusCheckDelay = 150 * time.Millisecond

// focusExpr is evaluated in the page. It follows document.activeElement into
// same-origin iframes and open shadow roots and returns only two booleans.
// Cross-origin iframes (contentDocument is null) and closed shadow roots are
// not inspectable and are reported as secure=false, editable=false.
const focusExpr = `(() => {
  try {
    let doc = document;
    let el = doc.activeElement;
    for (let depth = 0; el && depth < 16; depth++) {
      if (el.shadowRoot && el.shadowRoot.activeElement) { el = el.shadowRoot.activeElement; continue; }
      if ((el.tagName === 'IFRAME' || el.tagName === 'FRAME')) {
        let inner = null;
        try { inner = el.contentDocument; } catch (e) { inner = null; }
        if (inner && inner.activeElement) { el = inner.activeElement; continue; }
        return { secure: false, editable: false };
      }
      break;
    }
    if (!el) return { secure: false, editable: false };
    const tag = el.tagName;
    const type = (el.getAttribute('type') || 'text').toLowerCase();
    const ac = (el.getAttribute('autocomplete') || '').toLowerCase().split(/\s+/);
    let secure = false;
    if (tag === 'INPUT' && type === 'password') secure = true;
    if ((tag === 'INPUT' || tag === 'TEXTAREA') &&
        ac.some(t => t === 'current-password' || t === 'new-password' || t === 'one-time-code')) secure = true;
    const nonText = ['button','checkbox','radio','submit','reset','file','image','range','color','hidden'];
    let editable = false;
    if (tag === 'INPUT') editable = !nonText.includes(type) && !el.readOnly && !el.disabled;
    else if (tag === 'TEXTAREA') editable = !el.readOnly && !el.disabled;
    else if (el.isContentEditable) editable = true;
    return { secure: secure, editable: editable };
  } catch (e) {
    return { secure: false, editable: false };
  }
})()`

// parseFocusResult decodes a Runtime.evaluate response (returnByValue) into a
// FocusState.
func parseFocusResult(raw json.RawMessage) (FocusState, error) {
	var r struct {
		Result struct {
			Value *FocusState `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		return FocusState{}, err
	}
	if len(r.ExceptionDetails) > 0 || r.Result.Value == nil {
		return FocusState{}, errors.New("focus check failed")
	}
	return *r.Result.Value, nil
}

// checkFocus evaluates the focused element in the page.
func (t *UserTab) checkFocus(ctx context.Context) (FocusState, error) {
	raw, err := t.evaluate(ctx, focusExpr)
	if err != nil {
		return FocusState{}, err
	}
	return parseFocusResult(raw)
}

func (t *UserTab) evaluate(ctx context.Context, expr string) (json.RawMessage, error) {
	if t.evalFn != nil {
		return t.evalFn(ctx, expr)
	}
	return t.cdp.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression": expr, "returnByValue": true, "awaitPromise": false,
	})
}

// scheduleFocusCheck inspects the focused element shortly after an action that
// may have moved focus and broadcasts the result. It never blocks the caller
// (it is also invoked from the CDP read loop, where a Call would deadlock).
func (t *UserTab) scheduleFocusCheck() {
	go func() {
		time.Sleep(focusCheckDelay)
		if t.isClosed() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		st, err := t.checkFocus(ctx)
		if err != nil {
			return
		}
		msg, _ := json.Marshal(focusMessage{Type: "focus", FocusState: st})
		t.mu.Lock()
		t.lastFocus = msg
		t.broadcastReliableLocked(msg)
		t.mu.Unlock()
	}()
}
