package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// tabsChrome is a fake Chrome with a real, mutable target list.
type tabsChrome struct {
	srv     *httptest.Server
	profile string

	mu      sync.Mutex
	up      bool
	targets []string // page target ids in creation order
	next    int
	created []map[string]any  // params of every Target.createTarget
	closed  []string          // targets closed via Target.closeTarget before Browser.close
	openers map[string]string // target id -> opener id (for getTargets)
	methods []string          // every browser-level method that reached the fake
	autoAtt []map[string]any  // params of Target.setAutoAttach
}

func newTabsChrome(t *testing.T) *tabsChrome {
	f := &tabsChrome{up: true, profile: filepath.Join(t.TempDir(), "profile")}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/version", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		up := f.up
		f.mu.Unlock()
		if !up {
			http.Error(w, "down", 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{
			"Browser":              "Chrome/fake",
			"webSocketDebuggerUrl": "ws://" + r.Host + "/devtools/browser/x",
		})
	})
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		ts := []map[string]string{{"id": "worker", "type": "service_worker", "url": "chrome-extension://x"}}
		for _, id := range f.targets {
			ts = append(ts, map[string]string{"id": id, "type": "page", "url": "about:blank"})
		}
		json.NewEncoder(w).Encode(ts)
	})
	up := websocket.Upgrader{}
	mux.HandleFunc("/devtools/browser/x", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			var req struct {
				ID        int64          `json:"id"`
				Method    string         `json:"method"`
				Params    map[string]any `json:"params"`
				SessionID string         `json:"sessionId"`
			}
			if c.ReadJSON(&req) != nil {
				return
			}
			result := map[string]any{}
			f.mu.Lock()
			f.methods = append(f.methods, req.Method)
			switch req.Method {
			case "Target.getTargets":
				infos := []map[string]any{}
				for _, id := range f.targets {
					infos = append(infos, map[string]any{"targetId": id, "type": "page", "url": "about:blank", "openerId": f.openers[id]})
				}
				result["targetInfos"] = infos
			case "Target.attachToTarget":
				id, _ := req.Params["targetId"].(string)
				result["sessionId"] = "sess-" + id
			case "Target.setAutoAttach":
				f.autoAtt = append(f.autoAtt, req.Params)
			case "Target.createTarget":
				f.next++
				id := fmt.Sprintf("T%d", f.next)
				f.targets = append(f.targets, id)
				f.created = append(f.created, req.Params)
				result["targetId"] = id
			case "Target.closeTarget":
				id, _ := req.Params["targetId"].(string)
				f.closed = append(f.closed, id)
				for i, x := range f.targets {
					if x == id {
						f.targets = append(f.targets[:i], f.targets[i+1:]...)
						break
					}
				}
			case "Browser.close":
				f.up = false
				f.targets = nil
			}
			f.mu.Unlock()
			reply := map[string]any{"id": req.ID, "result": result}
			if req.SessionID != "" {
				reply["sessionId"] = req.SessionID // Chrome echoes the session
			}
			c.WriteJSON(reply)
		}
	})
	mux.HandleFunc("/devtools/page/", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			var req struct {
				ID int64 `json:"id"`
			}
			if c.ReadJSON(&req) != nil {
				return
			}
			if req.ID != 0 {
				c.WriteJSON(map[string]any{"id": req.ID, "result": map[string]any{}})
			}
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *tabsChrome) port() int {
	u, _ := url.Parse(f.srv.URL)
	p, _ := strconv.Atoi(u.Port())
	return p
}

func (f *tabsChrome) has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, x := range f.targets {
		if x == id {
			return true
		}
	}
	return false
}

func (f *tabsChrome) setTargets(ids ...string) {
	f.mu.Lock()
	f.targets = ids
	f.mu.Unlock()
}

func (f *tabsChrome) restart() {
	f.mu.Lock()
	f.up = true
	f.targets = []string{"restored1"} // session-restored leftover tab
	f.mu.Unlock()
}

func newAgentTestManager(t *testing.T, f *tabsChrome) *Manager {
	m := New(Config{
		ChromePath:   os.Args[0],
		Port:         f.port(),
		ProfileDir:   f.profile,
		RestoreGrace: 10 * time.Millisecond,
		StopTimeout:  400 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), func(string, []string) (Process, error) {
		f.restart()
		return &fakeProc{done: make(chan struct{})}, nil
	})
	m.inspect = func(int) (int, string, error) {
		return 1, "/fake/Chrome --user-data-dir=" + f.profile, nil
	}
	return m
}

func TestAgentTabAllocatesDistinctNewTabsAndReuses(t *testing.T) {
	f := newTabsChrome(t)
	f.setTargets("restored1", "restored2") // leftover tabs nobody owns
	m := newAgentTestManager(t, f)
	ctx := context.Background()

	user, err := m.UserTab(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.AgentTab(ctx, "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.AgentTab(ctx, "sess-b")
	if err != nil {
		t.Fatal(err)
	}
	if a.Reused || b.Reused {
		t.Fatal("first allocation must not be reported as reused")
	}
	ids := map[string]bool{user.TargetID(): true, "restored1": true, "restored2": true}
	for _, tab := range []AgentTab{a, b} {
		if ids[tab.TargetID] {
			t.Fatalf("agent tab %s aliases the user tab or a restored tab", tab.TargetID)
		}
		ids[tab.TargetID] = true
	}
	// Created in the background so the user tab keeps focus.
	f.mu.Lock()
	bg := f.created[len(f.created)-1]["background"]
	f.mu.Unlock()
	if bg != true {
		t.Fatalf("agent tab not created in background: %v", bg)
	}

	a2, err := m.AgentTab(ctx, "sess-a")
	if err != nil {
		t.Fatal(err)
	}
	if !a2.Reused || a2.TargetID != a.TargetID {
		t.Fatalf("repeat call: %+v, want reuse of %s", a2, a.TargetID)
	}
}

func TestAgentTabReallocatesWhenTabIsGone(t *testing.T) {
	f := newTabsChrome(t)
	m := newAgentTestManager(t, f)
	ctx := context.Background()
	a, err := m.AgentTab(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	// The tab disappears behind our back (closed by hand).
	f.mu.Lock()
	f.targets = nil
	f.mu.Unlock()
	a2, err := m.AgentTab(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if a2.Reused || a2.TargetID == a.TargetID || !f.has(a2.TargetID) {
		t.Fatalf("stale tab not re-allocated: old=%+v new=%+v", a, a2)
	}
}

func TestAgentTabAfterChromeStopAndStart(t *testing.T) {
	f := newTabsChrome(t)
	m := newAgentTestManager(t, f)
	ctx := context.Background()
	a, err := m.AgentTab(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if m.HasAgentTab("a") || m.HasAgentTab("b") || m.HasAgentTab("s") {
		t.Fatal("mapping not cleared on stop")
	}
	// Chrome comes back (restore leaves an unrelated tab); a stale target id
	// from before must not be served.
	a2, err := m.AgentTab(ctx, "s") // Start relaunches
	if err != nil {
		t.Fatal(err)
	}
	if a2.Reused || a2.TargetID == "restored1" || !f.has(a2.TargetID) {
		t.Fatalf("after restart: old=%+v new=%+v", a, a2)
	}
}

func TestAgentTabChromeRestartedWithoutStop(t *testing.T) {
	// E.g. Chrome crashed and was relaunched by something else: the mapped
	// id is absent from the new instance and must be re-allocated.
	f := newTabsChrome(t)
	m := newAgentTestManager(t, f)
	ctx := context.Background()
	a, _ := m.AgentTab(ctx, "s")
	f.restart()
	a2, err := m.AgentTab(ctx, "s")
	if err != nil {
		t.Fatal(err)
	}
	if a2.Reused || a2.TargetID == a.TargetID {
		t.Fatalf("stale id served: %+v %+v", a, a2)
	}
}

func TestCloseAgentTabClosesOnlyThatSessionsTab(t *testing.T) {
	f := newTabsChrome(t)
	f.setTargets("restored1")
	m := newAgentTestManager(t, f)
	ctx := context.Background()
	user, _ := m.UserTab(ctx)
	a, _ := m.AgentTab(ctx, "a")
	b, _ := m.AgentTab(ctx, "b")

	if err := m.CloseAgentTab(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if f.has(a.TargetID) {
		t.Fatal("a's tab still open")
	}
	for _, id := range []string{b.TargetID, user.TargetID(), "restored1"} {
		if !f.has(id) {
			t.Fatalf("tab %s was closed by closing a's tab", id)
		}
	}
	// Idempotent, and unknown sessions are a no-op.
	if err := m.CloseAgentTab(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.CloseAgentTab(ctx, "never-seen"); err != nil {
		t.Fatal(err)
	}
	// A fresh call after close allocates a new tab.
	a2, err := m.AgentTab(ctx, "a")
	if err != nil || a2.Reused || a2.TargetID == a.TargetID {
		t.Fatalf("realloc after close: %+v %v", a2, err)
	}
}

func TestCloseAgentTabWhenChromeIsDown(t *testing.T) {
	f := newTabsChrome(t)
	m := newAgentTestManager(t, f)
	ctx := context.Background()
	if _, err := m.AgentTab(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.up = false
	f.mu.Unlock()
	if err := m.CloseAgentTab(ctx, "a"); err != nil {
		t.Fatalf("close with chrome down: %v", err)
	}
}

func TestStopClosesAgentTabsBeforeBrowserClose(t *testing.T) {
	f := newTabsChrome(t)
	m := newAgentTestManager(t, f)
	ctx := context.Background()
	a, _ := m.AgentTab(ctx, "a")
	b, _ := m.AgentTab(ctx, "b")
	if _, _, err := m.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	closed := strings.Join(f.closed, ",")
	f.mu.Unlock()
	if !strings.Contains(closed, a.TargetID) || !strings.Contains(closed, b.TargetID) {
		t.Fatalf("agent tabs not closed before Browser.close (so Chrome would restore them): %q", closed)
	}
	if m.HasAgentTab("a") || m.HasAgentTab("b") || m.HasAgentTab("s") {
		t.Fatal("agent tabs not cleared")
	}
}

func TestAgentTabRejectsEmptyKey(t *testing.T) {
	f := newTabsChrome(t)
	m := newAgentTestManager(t, f)
	if _, err := m.AgentTab(context.Background(), ""); err == nil {
		t.Fatal("expected error for empty key")
	}
}
