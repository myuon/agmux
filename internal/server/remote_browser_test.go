package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"github.com/myuon/agmux/internal/browser"
)

type fakeRB struct {
	running bool
	stops   int
}

func (f *fakeRB) Start(ctx context.Context) (browser.Status, error) {
	f.running = true
	return browser.Status{Running: true}, nil
}
func (f *fakeRB) Status(ctx context.Context) browser.Status {
	return browser.Status{Running: f.running}
}
func (f *fakeRB) Stop(ctx context.Context) (browser.Status, browser.StopResult, error) {
	f.stops++
	f.running = false
	return browser.Status{}, browser.StopResult{Graceful: true}, nil
}
func (f *fakeRB) UserTab(ctx context.Context) (*browser.UserTab, error) {
	return nil, context.Canceled
}
func (f *fakeRB) Shutdown(ctx context.Context) {}

func newRBHandler(dev bool, rb RemoteBrowser) http.Handler {
	s := &Server{devMode: dev, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), remoteBrowser: rb}
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) { s.mountRemoteBrowser(r) })
	return r
}

func do(h http.Handler, method, path, remote string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestRemoteBrowserRejectsNonLoopback(t *testing.T) {
	h := newRBHandler(false, &fakeRB{})
	paths := [][2]string{
		{"GET", "/api/remote-browser/status"}, {"POST", "/api/remote-browser/start"},
		{"POST", "/api/remote-browser/stop"}, {"GET", "/api/remote-browser/ws"},
		{"GET", "/api/remote-browser/unknown"},
	}
	for _, remote := range []string{"192.168.1.20:5555", "10.0.0.1:1", "[2001:db8::1]:80", "garbage"} {
		for _, p := range paths {
			// Spoofed forwarding headers must not grant access.
			w := do(h, p[0], p[1], remote, map[string]string{
				"X-Forwarded-For": "127.0.0.1", "CF-Connecting-IP": "127.0.0.1", "X-Real-IP": "127.0.0.1",
			})
			if w.Code != http.StatusForbidden {
				t.Errorf("%s %s from %s = %d, want 403", p[0], p[1], remote, w.Code)
			}
		}
	}
}

func TestRemoteBrowserAllowsLoopback(t *testing.T) {
	for _, remote := range []string{"127.0.0.1:5555", "[::1]:5555"} {
		w := do(newRBHandler(false, &fakeRB{}), "GET", "/api/remote-browser/status", remote, nil)
		if w.Code != http.StatusOK {
			t.Errorf("loopback %s = %d", remote, w.Code)
		}
	}
}

func TestRemoteBrowserStateChangingOrigin(t *testing.T) {
	rb := &fakeRB{}
	h := newRBHandler(false, rb)
	r := func(origin string) int {
		hdr := map[string]string{}
		if origin != "" {
			hdr["Origin"] = origin
		}
		return do(h, "POST", "/api/remote-browser/stop", "127.0.0.1:1", hdr).Code
	}
	if c := r("https://evil.example"); c != http.StatusForbidden {
		t.Errorf("cross-site = %d", c)
	}
	if c := r("http://localhost:5173"); c != http.StatusForbidden { // dev origin only in dev mode
		t.Errorf("dev origin in prod = %d", c)
	}
	if rb.stops != 0 {
		t.Fatal("stop executed despite rejected origin")
	}
	if c := r(""); c != http.StatusOK {
		t.Errorf("no origin = %d", c)
	}
	if c := r("http://example.com"); c != http.StatusOK { // httptest Host is example.com
		t.Errorf("same-site = %d", c)
	}
}

func TestRemoteBrowserWSOrigin(t *testing.T) {
	rb := &fakeRB{}
	for _, dev := range []bool{false, true} {
		srv := httptest.NewServer(newRBHandler(dev, rb))
		host := strings.TrimPrefix(srv.URL, "http://")
		wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/remote-browser/ws"
		try := func(origin string) int {
			hdr := http.Header{}
			if origin != "" {
				hdr.Set("Origin", origin)
			}
			c, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
			if err == nil {
				c.Close()
				return 101
			}
			return resp.StatusCode
		}
		// Browser not running: 409 means the Origin check passed.
		if c := try("http://" + host); c != http.StatusConflict {
			t.Errorf("dev=%v same-origin = %d, want 409 (passed origin check)", dev, c)
		}
		if c := try(""); c != http.StatusForbidden {
			t.Errorf("dev=%v missing origin = %d", dev, c)
		}
		if c := try("https://evil.example"); c != http.StatusForbidden {
			t.Errorf("dev=%v cross origin = %d", dev, c)
		}
		if c := try("http://" + host + ".evil.example"); c != http.StatusForbidden {
			t.Errorf("dev=%v suffix origin = %d", dev, c)
		}
		want := http.StatusForbidden
		if dev {
			want = http.StatusConflict
		}
		if c := try("http://localhost:5173"); c != want {
			t.Errorf("dev=%v vite origin = %d, want %d", dev, c, want)
		}
		srv.Close()
	}
}

func TestRemoteBrowserUnconfigured(t *testing.T) {
	w := do(newRBHandler(false, nil), "GET", "/api/remote-browser/status", "127.0.0.1:1", nil)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d", w.Code)
	}
}
