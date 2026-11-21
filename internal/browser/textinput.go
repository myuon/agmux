package browser

import (
	"context"
	"encoding/json"
	"math"
)

// Text input that survives focus theft.
//
// Every CDP Input.* call (insertText, dispatchKeyEvent, mouse) makes Chrome
// re-assert page focus on the tab's widget. With some extensions installed
// (observed with iCloud Passwords, whose completion-list iframe lives in a
// separate extension process) that re-assertion moves focus to the top
// document: the field the user is typing into is blurred right after each
// insertText. Pages that track focus (e.g. the Apple ID sign-in form) then
// drop the input, so only the first character lands and the submit button
// stays disabled.
//
// To avoid it, text goes in through document.execCommand('insertText') in the
// frame of the field the user last tapped. execCommand fires trusted
// beforeinput/input events like real typing but does not go through the
// browser's input path, so page focus is not re-asserted. When focus has been
// lost to nowhere (the frame's active element is <body>), the tapped field is
// re-focused first. Anything unexpected falls back to Input.insertText.

// editableJS defines helpers shared by the page-side functions below.
const editableJS = `const editable = el => {
    if (!el || el.nodeType !== 1) return false;
    const tag = el.tagName;
    const type = (el.getAttribute('type') || 'text').toLowerCase();
    const nonText = ['button','checkbox','radio','submit','reset','file','image','range','color','hidden'];
    if (tag === 'INPUT') return !nonText.includes(type) && !el.readOnly && !el.disabled;
    if (tag === 'TEXTAREA') return !el.readOnly && !el.disabled;
    return el.isContentEditable;
  };
  const deepActive = d => {
    let a = d.activeElement;
    while (a && a.shadowRoot && a.shadowRoot.activeElement) a = a.shadowRoot.activeElement;
    return a;
  };
  const lost = d => { const a = deepActive(d); return !a || a === d.body || a === d.documentElement; };`

// tapFocusFn runs on the node under a tap after the click was dispatched. It
// returns true when the node is an editable field (so it becomes the text
// target) and focuses it if the tap left focus nowhere.
const tapFocusFn = `function() {
  ` + editableJS + `
  const el = this.nodeType === 1 ? this : this.parentElement;
  if (!editable(el)) return false;
  if (lost(el.ownerDocument)) el.focus();
  return true;
}`

// insertTextFn inserts text into the focused editable element of the tapped
// field's document. It returns false (caller falls back to Input.insertText)
// when focus moved somewhere else on purpose or the field is gone.
const insertTextFn = `function(text) {
  ` + editableJS + `
  const el = this.nodeType === 1 ? this : this.parentElement;
  if (!el || !el.isConnected) return false;
  const d = el.ownerDocument;
  if (!editable(deepActive(d))) {
    if (!lost(d) || !editable(el)) return false;
    el.focus();
  }
  return d.execCommand('insertText', false, text) === true;
}`

const textObjectGroup = "agmux-text-input"

// call is cdp.Call, overridable in tests.
func (t *UserTab) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if t.callFn != nil {
		return t.callFn(ctx, method, params)
	}
	return t.cdp.Call(ctx, method, params)
}

func (t *UserTab) setEditNode(id int64) {
	t.mu.Lock()
	t.editNode = id
	t.mu.Unlock()
}

func (t *UserTab) getEditNode() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.editNode
}

// callOnNode resolves a backend node and calls fn on it, returning the
// boolean result. Any failure is reported as false.
func (t *UserTab) callOnNode(ctx context.Context, backendNodeID int64, fn string, args ...any) bool {
	raw, err := t.call(ctx, "DOM.resolveNode", map[string]any{
		"backendNodeId": backendNodeID, "objectGroup": textObjectGroup,
	})
	if err != nil {
		return false
	}
	defer t.call(ctx, "Runtime.releaseObjectGroup", map[string]any{"objectGroup": textObjectGroup})
	var node struct {
		Object struct {
			ObjectID string `json:"objectId"`
		} `json:"object"`
	}
	if json.Unmarshal(raw, &node) != nil || node.Object.ObjectID == "" {
		return false
	}
	params := map[string]any{
		"objectId": node.Object.ObjectID, "functionDeclaration": fn, "returnByValue": true,
	}
	if len(args) > 0 {
		var a []map[string]any
		for _, v := range args {
			a = append(a, map[string]any{"value": v})
		}
		params["arguments"] = a
	}
	raw, err = t.call(ctx, "Runtime.callFunctionOn", params)
	if err != nil {
		return false
	}
	var res struct {
		Result struct {
			Value any `json:"value"`
		} `json:"result"`
		ExceptionDetails json.RawMessage `json:"exceptionDetails"`
	}
	if json.Unmarshal(raw, &res) != nil || len(res.ExceptionDetails) > 0 {
		return false
	}
	return res.Result.Value == true
}

// afterTap records the editable field under (x, y) as the text target and
// restores its focus if it was stolen. Failures only clear the target.
func (t *UserTab) afterTap(ctx context.Context, x, y float64) {
	t.setEditNode(0)
	raw, err := t.call(ctx, "DOM.getNodeForLocation", map[string]any{
		"x": int(math.Round(x)), "y": int(math.Round(y)),
	})
	if err != nil {
		return
	}
	var loc struct {
		BackendNodeID int64 `json:"backendNodeId"`
	}
	if json.Unmarshal(raw, &loc) != nil || loc.BackendNodeID == 0 {
		return
	}
	if t.callOnNode(ctx, loc.BackendNodeID, tapFocusFn) {
		t.setEditNode(loc.BackendNodeID)
	}
}

// insertText types text into the page, preferring execCommand in the tapped
// field's frame (see the comment at the top of this file).
func (t *UserTab) insertText(ctx context.Context, text string) error {
	if id := t.getEditNode(); id != 0 && t.callOnNode(ctx, id, insertTextFn, text) {
		return nil
	}
	_, err := t.call(ctx, "Input.insertText", map[string]any{"text": text})
	return err
}
