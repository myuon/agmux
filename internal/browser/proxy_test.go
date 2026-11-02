package browser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// proxyHarness serves ServeAgentCDP for one session key over a real websocket.
type proxyHarness struct {
	t      *testing.T
	f      *tabsChrome
	m      *Manager
	client *websocket.Conn
	nextID int64
}

func newProxyHarness(t *testing.T, f *tabsChrome, m *Manager, key string) *proxyHarness {
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		m.ServeAgentCDP(context.Background(), key, c)
	}))
	t.Cleanup(srv.Close)
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return &proxyHarness{t: t, f: f, m: m, client: c}
}

// call sends a command and returns the matching response (events are skipped).
func (h *proxyHarness) call(method string, params map[string]any, sessionID string) map[string]json.RawMessage {
	h.t.Helper()
	h.nextID++
	msg := map[string]any{"id": h.nextID, "method": method, "params": params}
	if sessionID != "" {
		msg["sessionId"] = sessionID
	}
	if err := h.client.WriteJSON(msg); err != nil {
		h.t.Fatal(err)
	}
	for {
		h.client.SetReadDeadline(time.Now().Add(3 * time.Second))
		var resp map[string]json.RawMessage
		if err := h.client.ReadJSON(&resp); err != nil {
			h.t.Fatalf("%s: %v", method, err)
		}
		if _, isEvent := resp["method"]; isEvent {
			continue
		}
		return resp
	}
}

func (h *proxyHarness) denied(method string, params map[string]any, sessionID string) bool {
	h.t.Helper()
	resp := h.call(method, params, sessionID)
	return resp["error"] != nil
}

func (h *proxyHarness) reachedUpstream(method string) bool {
	h.f.mu.Lock()
	defer h.f.mu.Unlock()
	for _, x := range h.f.methods {
		if x == method {
			return true
		}
	}
	return false
}

func setupProxy(t *testing.T) (*proxyHarness, *tabsChrome, *Manager, AgentTab, AgentTab) {
	f := newTabsChrome(t)
	f.setTargets("restored1")
	f.openers = map[string]string{}
	m := newAgentTestManager(t, f)
	ctx := context.Background()
	if _, err := m.UserTab(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := m.AgentTab(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.AgentTab(ctx, "b")
	if err != nil {
		t.Fatal(err)
	}
	return newProxyHarness(t, f, m, "a"), f, m, a, b
}

func TestProxyRequiresAllocatedTab(t *testing.T) {
	f := newTabsChrome(t)
	m := newAgentTestManager(t, f)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		defer c.Close()
		if err := m.ServeAgentCDP(context.Background(), "nobody", c); err != ErrNoAgentTab {
			t.Errorf("err = %v", err)
		}
	}))
	defer srv.Close()
	c, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	c.ReadMessage() // closes
}

func TestProxyGetTargetsOnlyShowsOwnTabs(t *testing.T) {
	h, _, _, a, _ := setupProxy(t)
	resp := h.call("Target.getTargets", nil, "")
	var res struct {
		TargetInfos []struct {
			TargetID string `json:"targetId"`
		} `json:"targetInfos"`
	}
	if err := json.Unmarshal(resp["result"], &res); err != nil {
		t.Fatal(err)
	}
	if len(res.TargetInfos) != 1 || res.TargetInfos[0].TargetID != a.TargetID {
		t.Fatalf("targets = %+v, want only %s (user tab, restored and other sessions' tabs must be hidden)", res.TargetInfos, a.TargetID)
	}
}

func TestProxyRefusesOtherTargetsAndBrowserWideCommands(t *testing.T) {
	h, f, m, _, b := setupProxy(t)
	user, _ := m.UserTab(context.Background())

	for _, id := range []string{b.TargetID, user.TargetID(), "restored1"} {
		for _, method := range []string{"Target.attachToTarget", "Target.closeTarget", "Target.activateTarget", "Target.getTargetInfo"} {
			if !h.denied(method, map[string]any{"targetId": id, "flatten": true}, "") {
				t.Errorf("%s on foreign target %s was not refused", method, id)
			}
		}
	}
	if h.denied("Target.getTargetInfo", map[string]any{"targetId": h.mustOwn()}, "") {
		t.Error("own target refused")
	}
	for _, method := range []string{"Browser.close", "Browser.crash", "Target.createBrowserContext", "Target.disposeBrowserContext",
		"Target.sendMessageToTarget", "Storage.clearCookies", "Browser.setDownloadBehavior"} {
		if !h.denied(method, map[string]any{}, "") {
			t.Errorf("%s not refused", method)
		}
	}
	if !h.denied("Target.createTarget", map[string]any{"url": "about:blank", "browserContextId": "x"}, "") {
		t.Error("createTarget in a foreign browser context not refused")
	}
	// Nothing refused may have reached Chrome.
	for _, method := range []string{"Browser.close", "Browser.crash", "Target.createBrowserContext", "Target.attachToTarget", "Target.closeTarget"} {
		if h.reachedUpstream(method) {
			t.Errorf("%s reached Chrome", method)
		}
	}
	if !f.has(b.TargetID) || !f.has("restored1") || !f.has(user.TargetID()) {
		t.Fatal("a foreign tab was closed")
	}
}

func (h *proxyHarness) mustOwn() string {
	h.m.agents.mu.Lock()
	defer h.m.agents.mu.Unlock()
	return h.m.agents.agents["a"].primary
}

func TestProxyUnknownSessionRefusedAndAttachRegistersSession(t *testing.T) {
	h, _, _, a, _ := setupProxy(t)
	if !h.denied("Page.navigate", map[string]any{"url": "http://x"}, "sess-"+"foreign") {
		t.Fatal("command on an unattached session was forwarded")
	}
	resp := h.call("Target.attachToTarget", map[string]any{"targetId": a.TargetID, "flatten": true}, "")
	var res struct {
		SessionID string `json:"sessionId"`
	}
	json.Unmarshal(resp["result"], &res)
	if res.SessionID == "" {
		t.Fatalf("attach failed: %s", resp["error"])
	}
	// Page-level commands on the attached session pass; Browser.close through it does not.
	if h.denied("Page.enable", map[string]any{}, res.SessionID) {
		t.Fatal("page command on own session refused")
	}
	if !h.denied("Browser.close", map[string]any{}, res.SessionID) {
		t.Fatal("Browser.close via page session not refused")
	}
	// Target.getTargets through the page session is filtered as well.
	resp = h.call("Target.getTargets", nil, res.SessionID)
	if strings.Contains(string(resp["result"]), "restored1") {
		t.Fatal("page-session getTargets leaked foreign tabs")
	}
}

func TestProxyCreateTargetAdoptsAndCloseReleases(t *testing.T) {
	h, f, m, _, _ := setupProxy(t)
	resp := h.call("Target.createTarget", map[string]any{"url": "about:blank"}, "")
	var res struct {
		TargetID string `json:"targetId"`
	}
	json.Unmarshal(resp["result"], &res)
	if res.TargetID == "" || !m.agents.owns("a", res.TargetID) {
		t.Fatalf("created tab not owned: %s", resp["result"])
	}
	if m.agents.owns("b", res.TargetID) {
		t.Fatal("created tab leaked to another session")
	}
	if h.denied("Target.closeTarget", map[string]any{"targetId": res.TargetID}, "") {
		t.Fatal("closing an own tab was refused")
	}
	if f.has(res.TargetID) || m.agents.owns("a", res.TargetID) {
		t.Fatal("tab not closed/released")
	}
}

func TestProxyAdoptsPopupsOfOwnTabsOnly(t *testing.T) {
	h, f, m, a, b := setupProxy(t)
	f.mu.Lock()
	f.targets = append(f.targets, "popupA", "popupB")
	f.openers["popupA"] = a.TargetID
	f.openers["popupB"] = b.TargetID
	f.mu.Unlock()
	resp := h.call("Target.getTargets", nil, "")
	s := string(resp["result"])
	if !strings.Contains(s, "popupA") || strings.Contains(s, "popupB") {
		t.Fatalf("popup visibility wrong: %s", s)
	}
	if !m.agents.owns("a", "popupA") || m.agents.owns("a", "popupB") {
		t.Fatal("popup ownership wrong")
	}
}

func TestProxyNeverAsksChromeToWaitForDebugger(t *testing.T) {
	h, f, _, a, _ := setupProxy(t)
	h.call("Target.setAutoAttach", map[string]any{"autoAttach": true, "waitForDebuggerOnStart": true, "flatten": true}, "")
	f.mu.Lock()
	got := f.autoAtt
	f.mu.Unlock()
	if len(got) != 1 || got[0]["waitForDebuggerOnStart"] != false {
		t.Fatalf("browser-level auto attach forwarded as %v", got)
	}
	_ = a
}

func TestProxyDropsSessionAndConnectionsWhenSessionEnds(t *testing.T) {
	h, _, m, a, _ := setupProxy(t)
	h.call("Target.getTargets", nil, "") // connection established and registered
	if err := m.CloseAgentTab(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	h.client.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := h.client.ReadMessage(); err == nil {
		t.Fatal("proxy connection stayed open after the session ended")
	}
	_ = a
}

func TestFilterUpstreamEvents(t *testing.T) {
	f := newTabsChrome(t)
	m := newAgentTestManager(t, f)
	ctx := context.Background()
	a, _ := m.AgentTab(ctx, "a")
	b, _ := m.AgentTab(ctx, "b")
	p := &agentProxy{key: "a", reg: &m.agents, sessions: map[string]string{}, pending: map[string]pendingCall{}}

	ev := func(method string, params any, sessionID string) *cdpEnvelope {
		raw, _ := json.Marshal(params)
		return &cdpEnvelope{Method: method, Params: raw, SessionID: sessionID}
	}
	type tc struct {
		name string
		env  *cdpEnvelope
		pass bool
	}
	for _, c := range []tc{
		{"own created", ev("Target.targetCreated", map[string]any{"targetInfo": map[string]any{"targetId": a.TargetID}}, ""), true},
		{"foreign created", ev("Target.targetCreated", map[string]any{"targetInfo": map[string]any{"targetId": b.TargetID}}, ""), false},
		{"foreign info changed", ev("Target.targetInfoChanged", map[string]any{"targetInfo": map[string]any{"targetId": "user"}}, ""), false},
		{"foreign attached", ev("Target.attachedToTarget", map[string]any{"sessionId": "S9", "targetInfo": map[string]any{"targetId": b.TargetID}}, ""), false},
		{"foreign destroyed", ev("Target.targetDestroyed", map[string]any{"targetId": b.TargetID}, ""), false},
		{"browser event", ev("Browser.downloadWillBegin", map[string]any{}, ""), false},
		{"unattached session event", ev("Page.frameNavigated", map[string]any{}, "S9"), false},
	} {
		if got := p.filterUpstream(c.env, []byte(`{}`)) != nil; got != c.pass {
			t.Errorf("%s: passed=%v want %v", c.name, got, c.pass)
		}
	}
	// own attach registers the session; its events then pass
	if p.filterUpstream(ev("Target.attachedToTarget", map[string]any{"sessionId": "S1", "targetInfo": map[string]any{"targetId": a.TargetID}}, ""), []byte(`{}`)) == nil {
		t.Fatal("own attach dropped")
	}
	if p.filterUpstream(ev("Page.frameNavigated", map[string]any{}, "S1"), []byte(`{}`)) == nil {
		t.Fatal("event of an attached own session dropped")
	}
}
