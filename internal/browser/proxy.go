package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ErrNoAgentTab is returned when a session connects to its CDP endpoint
// before it was allocated a tab.
var ErrNoAgentTab = errors.New("no tab allocated for this session; call get_logged_in_browser first")

// proxyIDBase marks request ids the proxy issues itself (not the client's).
const proxyIDBase = int64(1) << 40

// cdpEnvelope is the part of a CDP message the proxy looks at.
type cdpEnvelope struct {
	ID        *int64          `json:"id,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     json.RawMessage `json:"error,omitempty"`
}

// ServeAgentCDP relays one client (agent-browser) to Chrome's browser-level
// DevTools endpoint, exposing only the tabs owned by the agmux session key:
// other targets (the user's tab, restored tabs, other sessions' tabs) are
// invisible in target listings and discovery events, and every command that
// would reach them is refused. Tabs the client creates (or that its pages
// open) become owned by key. It blocks until either side closes.
//
// Messages are never logged; only method names of refused commands are.
func (m *Manager) ServeAgentCDP(ctx context.Context, key string, client *websocket.Conn) error {
	if !m.HasAgentTab(key) {
		return ErrNoAgentTab
	}
	ver, err := m.probe(ctx)
	if err != nil {
		return ErrNotRunning
	}
	d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	up, _, err := d.DialContext(ctx, ver.WebSocketDebuggerURL, nil)
	if err != nil {
		return fmt.Errorf("dial cdp: %w", err)
	}
	up.SetReadLimit(64 << 20)
	client.SetReadLimit(64 << 20)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Register so session end / Chrome stop can drop this connection.
	m.agents.mu.Lock()
	st := m.agents.state(key)
	connID := st.nextC
	st.nextC++
	st.cancels[connID] = func() {
		client.Close()
		up.Close()
		cancel()
	}
	m.agents.mu.Unlock()
	defer func() {
		m.agents.mu.Lock()
		if s := m.agents.agents[key]; s != nil {
			delete(s.cancels, connID)
		}
		m.agents.mu.Unlock()
	}()

	p := &agentProxy{
		key:      key,
		reg:      &m.agents,
		logger:   m.logger,
		client:   client,
		up:       up,
		sessions: map[string]string{},
		pending:  map[string]pendingCall{},
	}

	errc := make(chan error, 2)
	go func() { errc <- p.clientToUp() }()
	go func() { errc <- p.upToClient() }()
	select {
	case err = <-errc:
	case <-ctx.Done():
		err = ctx.Err()
	}
	client.Close()
	up.Close()
	return err
}

type pendingCall struct {
	method   string
	targetID string
}

type agentProxy struct {
	key    string
	reg    *agentRegistry
	logger *slog.Logger
	client *websocket.Conn
	up     *websocket.Conn

	cmu sync.Mutex // serializes writes to client
	umu sync.Mutex // serializes writes to up

	mu       sync.Mutex
	sessions map[string]string // allowed CDP sessionId -> targetId
	pending  map[string]pendingCall
	nextOwn  int64
}

func (p *agentProxy) writeClient(data []byte) error {
	p.cmu.Lock()
	defer p.cmu.Unlock()
	p.client.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return p.client.WriteMessage(websocket.TextMessage, data)
}

func (p *agentProxy) writeUp(data []byte) error {
	if p.up == nil {
		return nil
	}
	p.umu.Lock()
	defer p.umu.Unlock()
	p.up.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return p.up.WriteMessage(websocket.TextMessage, data)
}

func (p *agentProxy) sessionAllowed(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.sessions[id]
	return ok
}

func (p *agentProxy) allowSession(sessionID, targetID string) {
	p.mu.Lock()
	p.sessions[sessionID] = targetID
	p.mu.Unlock()
}

func pkey(sessionID string, id int64) string { return sessionID + "\x00" + strconv.FormatInt(id, 10) }

func (p *agentProxy) deny(env *cdpEnvelope, msg string) error {
	if env.ID == nil {
		return nil
	}
	p.logger.Debug("agent cdp command refused", "method", env.Method)
	out := map[string]any{
		"id":    *env.ID,
		"error": map[string]any{"code": -32000, "message": "agmux: " + msg},
	}
	if env.SessionID != "" {
		out["sessionId"] = env.SessionID
	}
	b, _ := json.Marshal(out)
	return p.writeClient(b)
}

// browserMethods are the non-Target commands allowed without a page session.
var browserMethods = map[string]bool{
	"Browser.getVersion": true,
}

// pageBrowserMethods are Browser.* commands harmless through a page session.
var pageBrowserMethods = map[string]bool{
	"Browser.getWindowForTarget": true,
	"Browser.getWindowBounds":    true,
	"Browser.setWindowBounds":    true,
}

func (p *agentProxy) clientToUp() error {
	for {
		_, data, err := p.client.ReadMessage()
		if err != nil {
			return err
		}
		var env cdpEnvelope
		if json.Unmarshal(data, &env) != nil || env.ID == nil || env.Method == "" {
			continue // not a command: ignore
		}
		out, err := p.filterCommand(&env, data)
		if err != nil {
			return err
		}
		if out == nil {
			continue
		}
		if err := p.writeUp(out); err != nil {
			return err
		}
	}
}

// filterCommand returns the bytes to forward upstream, or nil when the command
// was refused (and an error reply already sent).
func (p *agentProxy) filterCommand(env *cdpEnvelope, raw []byte) ([]byte, error) {
	if env.SessionID != "" && !p.sessionAllowed(env.SessionID) {
		return nil, p.deny(env, "unknown session")
	}
	if strings.HasPrefix(env.Method, "Target.") {
		return p.filterTargetCommand(env, raw)
	}
	if env.SessionID == "" {
		if !browserMethods[env.Method] {
			return nil, p.deny(env, env.Method+" is not available for agent sessions")
		}
		return raw, nil
	}
	// Browser.* issued through a page session is still browser-wide
	// (Browser.close, setDownloadBehavior, ...): allow only window helpers.
	if strings.HasPrefix(env.Method, "Browser.") && !browserMethods[env.Method] && !pageBrowserMethods[env.Method] {
		return nil, p.deny(env, env.Method+" is not available for agent sessions")
	}
	return raw, nil
}

func (p *agentProxy) filterTargetCommand(env *cdpEnvelope, raw []byte) ([]byte, error) {
	var params struct {
		TargetID  string `json:"targetId"`
		SessionID string `json:"sessionId"`
	}
	if len(env.Params) > 0 {
		json.Unmarshal(env.Params, &params)
	}
	track := func(targetID string) {
		p.mu.Lock()
		p.pending[pkey(env.SessionID, *env.ID)] = pendingCall{method: env.Method, targetID: targetID}
		p.mu.Unlock()
	}
	owned := func(id string) bool { return id != "" && p.reg.owns(p.key, id) }

	switch env.Method {
	case "Target.getTargets", "Target.createTarget":
		if env.Method == "Target.createTarget" {
			// Never place a tab in another browser context: only the default
			// context holds the shared login.
			var obj map[string]json.RawMessage
			if json.Unmarshal(env.Params, &obj) == nil {
				if _, ok := obj["browserContextId"]; ok {
					return nil, p.deny(env, "browser contexts are not available for agent sessions")
				}
			}
		}
		track("")
		return raw, nil
	case "Target.getTargetInfo":
		if params.TargetID == "" && env.SessionID != "" {
			return raw, nil // the session's own target
		}
		if !owned(params.TargetID) {
			return nil, p.deny(env, "target not found")
		}
		return raw, nil
	case "Target.attachToTarget":
		if !owned(params.TargetID) {
			return nil, p.deny(env, "target not found")
		}
		track(params.TargetID)
		return raw, nil
	case "Target.activateTarget":
		if !owned(params.TargetID) {
			return nil, p.deny(env, "target not found")
		}
		return raw, nil
	case "Target.closeTarget":
		if !owned(params.TargetID) {
			return nil, p.deny(env, "target not found")
		}
		track(params.TargetID)
		return raw, nil
	case "Target.detachFromTarget":
		if params.SessionID != "" && !p.sessionAllowed(params.SessionID) {
			return nil, p.deny(env, "unknown session")
		}
		if params.SessionID == "" && !owned(params.TargetID) {
			return nil, p.deny(env, "target not found")
		}
		return raw, nil
	case "Target.setDiscoverTargets":
		return raw, nil
	case "Target.setAutoAttach":
		if env.SessionID != "" {
			return raw, nil // scoped to that page's own children
		}
		// Browser-wide auto-attach would pause every new tab (the user's
		// included) until a debugger resumes it, but the client never learns
		// about tabs it may not see. Never ask Chrome to wait.
		var obj map[string]json.RawMessage
		if json.Unmarshal(env.Params, &obj) != nil {
			return nil, p.deny(env, "invalid params")
		}
		obj["waitForDebuggerOnStart"] = json.RawMessage("false")
		np, _ := json.Marshal(obj)
		var full map[string]json.RawMessage
		if json.Unmarshal(raw, &full) != nil {
			return nil, p.deny(env, "invalid message")
		}
		full["params"] = np
		out, _ := json.Marshal(full)
		return out, nil
	}
	return nil, p.deny(env, env.Method+" is not available for agent sessions")
}

func (p *agentProxy) upToClient() error {
	for {
		_, data, err := p.up.ReadMessage()
		if err != nil {
			return err
		}
		var env cdpEnvelope
		if json.Unmarshal(data, &env) != nil {
			continue
		}
		out := p.filterUpstream(&env, data)
		if out == nil {
			continue
		}
		if err := p.writeClient(out); err != nil {
			return err
		}
	}
}

type cdpTargetInfo struct {
	TargetID string `json:"targetId"`
	Type     string `json:"type"`
	OpenerID string `json:"openerId"`
}

// visible reports whether a target may be shown to the client, adopting
// targets opened by an owned page (popups, window.open, target=_blank).
func (p *agentProxy) visible(ti cdpTargetInfo) bool {
	if ti.TargetID == "" {
		return false
	}
	if p.reg.owns(p.key, ti.TargetID) {
		return true
	}
	if ti.OpenerID != "" && p.reg.owns(p.key, ti.OpenerID) {
		p.reg.adopt(p.key, ti.TargetID)
		return true
	}
	return false
}

// filterUpstream returns the bytes to deliver to the client, or nil to drop.
func (p *agentProxy) filterUpstream(env *cdpEnvelope, raw []byte) []byte {
	if env.ID != nil {
		return p.filterResponse(env, raw)
	}
	switch env.Method {
	case "Target.attachedToTarget":
		var ev struct {
			SessionID  string        `json:"sessionId"`
			TargetInfo cdpTargetInfo `json:"targetInfo"`
		}
		json.Unmarshal(env.Params, &ev)
		allowed := false
		if env.SessionID != "" {
			allowed = p.sessionAllowed(env.SessionID) // child of an allowed page
			if allowed {
				p.reg.adopt(p.key, ev.TargetInfo.TargetID)
			}
		} else {
			allowed = p.visible(ev.TargetInfo)
		}
		if !allowed {
			// Release the session Chrome created for us: the client will
			// never see it. Our own ids are above proxyIDBase and their
			// replies are dropped.
			if ev.SessionID != "" && env.SessionID == "" {
				p.detachUpstream(ev.SessionID)
			}
			return nil
		}
		p.allowSession(ev.SessionID, ev.TargetInfo.TargetID)
		return raw
	case "Target.detachedFromTarget":
		var ev struct {
			SessionID string `json:"sessionId"`
		}
		json.Unmarshal(env.Params, &ev)
		if !p.sessionAllowed(ev.SessionID) {
			return nil
		}
		p.mu.Lock()
		delete(p.sessions, ev.SessionID)
		p.mu.Unlock()
		return raw
	case "Target.targetCreated", "Target.targetInfoChanged":
		var ev struct {
			TargetInfo cdpTargetInfo `json:"targetInfo"`
		}
		json.Unmarshal(env.Params, &ev)
		if p.visible(ev.TargetInfo) {
			return raw
		}
		return nil
	case "Target.targetDestroyed", "Target.targetCrashed":
		var ev struct {
			TargetID string `json:"targetId"`
		}
		json.Unmarshal(env.Params, &ev)
		if !p.reg.owns(p.key, ev.TargetID) {
			return nil
		}
		if env.Method == "Target.targetDestroyed" {
			p.reg.release(p.key, ev.TargetID)
		}
		return raw
	}
	if env.SessionID != "" {
		if p.sessionAllowed(env.SessionID) {
			return raw
		}
		return nil
	}
	return nil // other browser-level events are not for agents
}

func (p *agentProxy) filterResponse(env *cdpEnvelope, raw []byte) []byte {
	if *env.ID >= proxyIDBase {
		return nil // reply to one of our own commands
	}
	p.mu.Lock()
	pc, ok := p.pending[pkey(env.SessionID, *env.ID)]
	delete(p.pending, pkey(env.SessionID, *env.ID))
	p.mu.Unlock()
	if !ok || env.Error != nil {
		return raw
	}
	switch pc.method {
	case "Target.getTargets":
		var res struct {
			TargetInfos []json.RawMessage `json:"targetInfos"`
		}
		if json.Unmarshal(env.Result, &res) != nil {
			return nil
		}
		kept := []json.RawMessage{}
		for _, t := range res.TargetInfos {
			var ti cdpTargetInfo
			json.Unmarshal(t, &ti)
			if p.visible(ti) {
				kept = append(kept, t)
			}
		}
		var full map[string]json.RawMessage
		if json.Unmarshal(raw, &full) != nil {
			return nil
		}
		full["result"], _ = json.Marshal(map[string]any{"targetInfos": kept})
		out, _ := json.Marshal(full)
		return out
	case "Target.createTarget":
		var res struct {
			TargetID string `json:"targetId"`
		}
		json.Unmarshal(env.Result, &res)
		if res.TargetID != "" {
			p.reg.adopt(p.key, res.TargetID)
		}
	case "Target.attachToTarget":
		var res struct {
			SessionID string `json:"sessionId"`
		}
		json.Unmarshal(env.Result, &res)
		if res.SessionID != "" {
			p.allowSession(res.SessionID, pc.targetID)
		}
	case "Target.closeTarget":
		p.reg.release(p.key, pc.targetID)
	}
	return raw
}

func (p *agentProxy) detachUpstream(sessionID string) {
	p.mu.Lock()
	p.nextOwn++
	id := proxyIDBase + p.nextOwn
	p.mu.Unlock()
	b, _ := json.Marshal(map[string]any{
		"id": id, "method": "Target.detachFromTarget", "params": map[string]any{"sessionId": sessionID},
	})
	p.writeUp(b)
}
