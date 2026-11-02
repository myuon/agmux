package browser

import (
	"context"
	"encoding/json"
	"errors"
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

// fakeChrome emulates the parts of Chrome's DevTools HTTP/WS surface we use.
type fakeChrome struct {
	t          *testing.T
	srv        *httptest.Server
	mu         sync.Mutex
	up         bool
	ignoreStop bool // ignore Browser.close (simulates a hung Chrome)
	profile    string
	calls      []string // page-target methods in order
	pages      int
	closeCalls int
}

func newFakeChrome(t *testing.T, profile string) *fakeChrome {
	f := &fakeChrome{t: t, up: true, profile: profile, pages: 1}
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
		n := f.pages
		f.mu.Unlock()
		var ts []map[string]string
		for i := 0; i < n; i++ {
			ts = append(ts, map[string]string{"id": "p" + strconv.Itoa(i), "type": "page", "url": "about:blank"})
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
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if c.ReadJSON(&req) != nil {
				return
			}
			result := map[string]any{}
			switch req.Method {
			case "Target.createTarget":
				f.mu.Lock()
				f.pages++
				f.mu.Unlock()
				result["targetId"] = "user-tab"
			case "Target.closeTarget":
				f.mu.Lock()
				f.pages--
				f.mu.Unlock()
			case "Browser.close":
				f.mu.Lock()
				f.closeCalls++
				if !f.ignoreStop {
					f.up = false
				}
				f.mu.Unlock()
			}
			c.WriteJSON(map[string]any{"id": req.ID, "result": result})
		}
	})
	mux.HandleFunc("/devtools/page/user-tab", func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			var req struct {
				ID     int64          `json:"id"`
				Method string         `json:"method"`
				Params map[string]any `json:"params"`
			}
			if c.ReadJSON(&req) != nil {
				return
			}
			f.mu.Lock()
			f.calls = append(f.calls, req.Method)
			f.mu.Unlock()
			if req.ID != 0 {
				c.WriteJSON(map[string]any{"id": req.ID, "result": map[string]any{}})
			}
			if req.Method == "Page.startScreencast" {
				c.WriteJSON(map[string]any{
					"method": "Page.screencastFrame",
					"params": map[string]any{
						"data":      "QUJD",
						"sessionId": 7,
						"metadata":  map[string]any{"deviceWidth": 390, "deviceHeight": 844},
					},
				})
			}
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeChrome) port() int {
	u, _ := url.Parse(f.srv.URL)
	p, _ := strconv.Atoi(u.Port())
	return p
}

func (f *fakeChrome) setUp(v bool) { f.mu.Lock(); f.up = v; f.mu.Unlock() }

type fakeProc struct {
	done   chan struct{}
	once   sync.Once
	killed bool
	onKill func()
}

func (p *fakeProc) PID() int { return 4242 }
func (p *fakeProc) Kill() error {
	p.killed = true
	if p.onKill != nil {
		p.onKill()
	}
	p.once.Do(func() { close(p.done) })
	return nil
}
func (p *fakeProc) Done() <-chan struct{} { return p.done }

func testManager(t *testing.T, f *fakeChrome, l Launcher) *Manager {
	dir := filepath.Join(t.TempDir(), "profile")
	if f.profile == "" {
		f.profile = dir
	}
	m := New(Config{
		ChromePath:   os.Args[0], // exists
		Port:         f.port(),
		ProfileDir:   f.profile,
		ReadyTimeout: 2 * time.Second,
		StopTimeout:  400 * time.Millisecond,
		RestoreGrace: 50 * time.Millisecond,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), l)
	m.inspect = func(int) (int, string, error) {
		return 1, "/fake/Chrome --remote-debugging-port=1 --user-data-dir=" + f.profile + " --no-first-run", nil
	}
	return m
}

func TestStopUsesBrowserCloseNotKill(t *testing.T) {
	f := newFakeChrome(t, filepath.Join(t.TempDir(), "profile"))
	proc := &fakeProc{done: make(chan struct{})}
	m := testManager(t, f, func(string, []string) (Process, error) { return proc, nil })
	f.setUp(false) // not running yet -> Start launches
	f.mu.Lock()
	f.mu.Unlock()
	launched := make(chan struct{})
	m.launcher = func(string, []string) (Process, error) { f.setUp(true); close(launched); return proc, nil }

	if _, err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, res, err := m.Stop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Graceful || proc.killed {
		t.Fatalf("expected graceful stop without kill: %+v killed=%v", res, proc.killed)
	}
	if f.closeCalls != 1 {
		t.Fatalf("Browser.close calls = %d", f.closeCalls)
	}
	if st.Running {
		t.Fatal("still running after stop")
	}
}

func TestStopFallsBackToKillAfterTimeout(t *testing.T) {
	f := newFakeChrome(t, filepath.Join(t.TempDir(), "profile"))
	f.ignoreStop = true
	f.setUp(false)
	proc := &fakeProc{done: make(chan struct{})}
	proc.onKill = func() { f.setUp(false) }
	m := testManager(t, f, nil)
	m.launcher = func(string, []string) (Process, error) { f.setUp(true); return proc, nil }
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, res, err := m.Stop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Graceful || !proc.killed {
		t.Fatalf("expected SIGKILL fallback: %+v killed=%v", res, proc.killed)
	}
}

func TestStopNeverKillsUnownedChrome(t *testing.T) {
	f := newFakeChrome(t, filepath.Join(t.TempDir(), "profile"))
	f.ignoreStop = true
	m := testManager(t, f, nil)
	if _, err := m.Start(context.Background()); err != nil { // reattach
		t.Fatal(err)
	}
	if _, _, err := m.Stop(context.Background()); err == nil {
		t.Fatal("expected error when unowned chrome will not stop")
	}
}

func TestStartReattachesWithoutLaunching(t *testing.T) {
	f := newFakeChrome(t, filepath.Join(t.TempDir(), "profile"))
	m := testManager(t, f, func(string, []string) (Process, error) {
		t.Fatal("must not launch a second Chrome")
		return nil, nil
	})
	st, err := m.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.Running || st.Owned {
		t.Fatalf("status = %+v", st)
	}
}

func TestStartRefusesForeignProfile(t *testing.T) {
	f := newFakeChrome(t, "/somewhere/else")
	m := testManager(t, f, nil)
	m.cfg.ProfileDir = filepath.Join(t.TempDir(), "mine")
	if _, err := m.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "different profile") {
		t.Fatalf("err = %v", err)
	}
}

func TestStartChromeMissing(t *testing.T) {
	f := newFakeChrome(t, filepath.Join(t.TempDir(), "p"))
	f.setUp(false)
	m := testManager(t, f, nil)
	m.cfg.ChromePath = "/nonexistent/chrome"
	_, err := m.Start(context.Background())
	if !errors.Is(err, ErrChromeNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestLaunchArgsAndProfilePerm(t *testing.T) {
	f := newFakeChrome(t, "")
	f.setUp(false)
	var got []string
	proc := &fakeProc{done: make(chan struct{})}
	m := testManager(t, f, func(_ string, args []string) (Process, error) {
		got = args
		f.setUp(true)
		return proc, nil
	})
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := " " + strings.Join(got, " ") + " "
	for _, want := range []string{
		fmt.Sprintf("--remote-debugging-port=%d", f.port()),
		"--remote-debugging-address=127.0.0.1",
		"--user-data-dir=" + f.profile,
		"--restore-last-session", "--no-first-run", "--no-default-browser-check",
	} {
		if !strings.Contains(joined, " "+want+" ") {
			t.Errorf("missing arg %s in %v", want, got)
		}
	}
	for _, a := range got {
		if strings.Contains(a, "headless") {
			t.Errorf("headless arg present: %s", a)
		}
	}
	fi, err := os.Stat(f.profile)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("profile perm = %v err=%v", fi.Mode().Perm(), err)
	}
}

func TestScreencastFocusEmulationBeforeStartAndAck(t *testing.T) {
	f := newFakeChrome(t, filepath.Join(t.TempDir(), "profile"))
	m := testManager(t, f, nil)
	if _, err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	tab, err := m.UserTab(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	sub, err := tab.Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	select {
	case msg := <-sub.C:
		var fm frameMessage
		if err := json.Unmarshal(msg, &fm); err != nil || fm.Type != "frame" || fm.Data != "QUJD" || fm.Metadata.DeviceWidth != 390 {
			t.Fatalf("bad frame %s (%v)", msg, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		calls := append([]string(nil), f.calls...)
		f.mu.Unlock()
		if len(calls) > 0 && calls[len(calls)-1] == "Page.screencastFrameAck" {
			idx := func(s string) int {
				for i, c := range calls {
					if c == s {
						return i
					}
				}
				return -1
			}
			if a, b := idx("Emulation.setFocusEmulationEnabled"), idx("Page.startScreencast"); a < 0 || b < 0 || a > b {
				t.Fatalf("focus emulation must precede startScreencast: %v", calls)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("frame not acked: %v", calls)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestValidateNavigateURL(t *testing.T) {
	for _, ok := range []string{"https://example.com/a?b=1", "http://localhost:8080", "about:blank"} {
		if err := validateNavigateURL(ok); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"javascript:alert(1)", "file:///etc/passwd", "chrome://settings", "data:text/html,x", "", "example.com"} {
		if err := validateNavigateURL(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}
