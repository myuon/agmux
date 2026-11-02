package browser

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// Why a proxy: agent-browser cannot be bound to one tab. Given a page-level
// DevTools URL (ws://.../devtools/page/<id>) it ignores the id and attaches to
// a tab of its own choosing (verified: it picks the first-created page, or
// whatever the browser lists first after tab activation, so two sessions end
// up on the same tab, or on the user's). The only mechanism that actually
// isolates sessions is to give each agmux session its own CDP endpoint that
// only exposes the tabs that session owns (see proxy.go). The tab bookkeeping
// for that lives here.

// agentState is the per-agmux-session tab ownership. Guarded by agentMu.
type agentState struct {
	primary string              // the tab allocated by AgentTab
	owned   map[string]struct{} // primary + tabs the session opened itself
	cancels map[int]func()      // live proxy connections
	nextC   int
}

// AgentTab describes the tab dedicated to one agmux session.
type AgentTab struct {
	TargetID string `json:"targetId"`
	Reused   bool   `json:"reused"` // false when a new tab was created for this call
}

type agentRegistry struct {
	mu     sync.Mutex
	agents map[string]*agentState
}

func (r *agentRegistry) state(key string) *agentState {
	if r.agents == nil {
		r.agents = map[string]*agentState{}
	}
	st := r.agents[key]
	if st == nil {
		st = &agentState{owned: map[string]struct{}{}, cancels: map[int]func(){}}
		r.agents[key] = st
	}
	return st
}

// owns reports whether the session key owns the target.
func (r *agentRegistry) owns(key, id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := r.agents[key]
	if st == nil {
		return false
	}
	_, ok := st.owned[id]
	return ok
}

func (r *agentRegistry) adopt(key, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st := r.agents[key]; st != nil {
		st.owned[id] = struct{}{}
	}
}

func (r *agentRegistry) release(key, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if st := r.agents[key]; st != nil {
		delete(st.owned, id)
	}
}

// AgentTab returns the tab dedicated to key (an agmux session id), starting
// Chrome if needed. The same live tab is returned on repeated calls. A tab
// that no longer exists (closed by hand, or Chrome restarted since) is
// replaced by a freshly created one. A tab is always created new: an existing
// tab (the user's tab, session-restored leftovers, another session's tab) is
// never handed out.
//
// The mapping is held in memory only. Chrome is closed on agmux shutdown, and
// agent tabs are closed with it, so there is nothing worth persisting; after
// an agmux crash with Chrome left running, tabs of the previous process are
// orphaned (nobody owns them, no session can reach them through the proxy)
// and sessions simply get new ones.
func (m *Manager) AgentTab(ctx context.Context, key string) (AgentTab, error) {
	if key == "" {
		return AgentTab{}, errors.New("agent tab: empty session key")
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.startLocked(ctx); err != nil {
		return AgentTab{}, err
	}

	m.agents.mu.Lock()
	st := m.agents.state(key)
	primary := st.primary
	m.agents.mu.Unlock()
	if primary != "" {
		if m.pageExistsLocked(ctx, primary) && !m.isUserTargetLocked(primary) {
			return AgentTab{TargetID: primary, Reused: true}, nil
		}
		m.agents.release(key, primary) // stale: re-allocate below
	}

	bc, err := m.browserClient(ctx)
	if err != nil {
		return AgentTab{}, err
	}
	defer bc.Close()
	// background: do not steal focus from the user tab (its screencast would
	// otherwise be hidden/throttled).
	raw, err := bc.Call(ctx, "Target.createTarget", map[string]any{"url": "about:blank", "background": true})
	if err != nil {
		return AgentTab{}, err
	}
	var res struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.TargetID == "" {
		return AgentTab{}, errors.New("create agent tab: no target id")
	}
	if m.isUserTargetLocked(res.TargetID) {
		return AgentTab{}, errors.New("create agent tab: target collides with the user tab")
	}
	m.agents.mu.Lock()
	defer m.agents.mu.Unlock()
	for k, other := range m.agents.agents {
		if _, taken := other.owned[res.TargetID]; taken && k != key {
			return AgentTab{}, errors.New("create agent tab: target already owned by another session")
		}
	}
	st = m.agents.state(key)
	st.primary = res.TargetID
	st.owned[res.TargetID] = struct{}{}
	return AgentTab{TargetID: res.TargetID}, nil
}

// HasAgentTab reports whether key has been allocated a tab (the proxy only
// serves sessions that called AgentTab).
func (m *Manager) HasAgentTab(key string) bool {
	m.agents.mu.Lock()
	defer m.agents.mu.Unlock()
	st := m.agents.agents[key]
	return st != nil && st.primary != ""
}

// CloseAgentTab closes every tab owned by key (the allocated one and any the
// agent opened) and drops its proxy connections. It is a no-op when there is
// nothing for key or Chrome is not running.
func (m *Manager) CloseAgentTab(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.agents.mu.Lock()
	st := m.agents.agents[key]
	delete(m.agents.agents, key)
	m.agents.mu.Unlock()
	if st == nil {
		return nil
	}
	for _, cancel := range st.cancels {
		cancel()
	}
	if len(st.owned) == 0 {
		return nil
	}
	bc, err := m.browserClient(ctx)
	if err != nil {
		return nil // Chrome is gone, so are its tabs
	}
	defer bc.Close()
	var firstErr error
	for id := range st.owned {
		if !m.pageExistsLocked(ctx, id) {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, err := bc.Call(cctx, "Target.closeTarget", map[string]any{"targetId": id})
		cancel()
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// closeAllAgentTabsLocked closes all agent-owned tabs using bc and clears the
// registry (used by Stop). m.mu must be held.
func (m *Manager) closeAllAgentTabsLocked(ctx context.Context, bc *cdpClient) {
	m.agents.mu.Lock()
	all := m.agents.agents
	m.agents.agents = map[string]*agentState{}
	m.agents.mu.Unlock()
	for _, st := range all {
		for _, cancel := range st.cancels {
			cancel()
		}
		if bc == nil {
			continue
		}
		for id := range st.owned {
			cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			bc.Call(cctx, "Target.closeTarget", map[string]any{"targetId": id})
			cancel()
		}
	}
}

func (m *Manager) isUserTargetLocked(id string) bool {
	return m.tab != nil && m.tab.TargetID() == id
}

func (m *Manager) pageExistsLocked(ctx context.Context, id string) bool {
	ts, err := m.listTargets(ctx)
	if err != nil {
		return false
	}
	for _, t := range ts {
		if t.ID == id && t.Type == "page" {
			return true
		}
	}
	return false
}
