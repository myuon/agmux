package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	"github.com/myuon/agmux/internal/config"
)

func newGuardServer(dev bool) *Server {
	s := &Server{devMode: dev, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.SetAllowedHosts([]string{"agmux.example.test"})
	return s
}

func guardHandler(s *Server) http.Handler {
	return s.requestGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
}

func TestRequestGuardHost(t *testing.T) {
	tests := []struct {
		host string
		want int
	}{
		{"localhost", 204},
		{"localhost:4321", 204},
		{"127.0.0.1:4321", 204},
		{"[::1]:4321", 204},
		{"[::1]", 204},
		{"192.168.1.20:4321", 204},
		{"100.64.0.5", 204},
		{"agmux.example.test", 204},
		{"agmux.example.test:443", 204},
		{"LocalHost:4321", 204},
		{"AGMUX.Example.TEST", 204},
		{"evil.example.test", 403},
		{"evil.example.test:4321", 403},
		{"", 403},
		{"localhost.", 403},
		{"evil.com.", 403},
		{"agmux.example.test.", 403},
		{"127.0.0.1.evil.test", 403},
		{"1.2.3.4.nip.io", 403},
		{"sub.agmux.example.test", 403},
		{"localhost.evil.test", 403},
	}
	h := guardHandler(newGuardServer(false))
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = tt.host
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("host %q: status %d, want %d", tt.host, w.Code, tt.want)
			}
			if tt.want == 403 && !strings.Contains(w.Body.String(), "server.allowed_hosts") {
				t.Errorf("403 body should name server.allowed_hosts: %q", w.Body.String())
			}
		})
	}
}

func TestRequestGuardOrigin(t *testing.T) {
	tests := []struct {
		name   string
		method string
		host   string
		origin string
		dev    bool
		want   int
	}{
		{"post same host", "POST", "agmux.example.test", "https://agmux.example.test", false, 204},
		{"post same host with port", "POST", "localhost:4321", "http://localhost:4321", false, 204},
		{"post cross-site", "POST", "localhost:4321", "https://evil.example.test", false, 403},
		{"post same host different port", "POST", "localhost:4321", "http://localhost:9999", false, 403},
		{"post null origin", "POST", "localhost:4321", "null", false, 403},
		{"post absent origin", "POST", "localhost:4321", "", false, 204},
		{"put cross-site", "PUT", "localhost:4321", "https://evil.example.test", false, 403},
		{"delete cross-site", "DELETE", "localhost:4321", "https://evil.example.test", false, 403},
		{"get cross-site is not state-changing", "GET", "localhost:4321", "https://evil.example.test", false, 204},
		{"post dev origin in dev mode", "POST", "localhost:4321", devOrigin, true, 204},
		{"post dev origin outside dev mode", "POST", "localhost:4321", devOrigin, false, 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := guardHandler(newGuardServer(tt.dev))
			req := httptest.NewRequest(tt.method, "/api/x", strings.NewReader("{}"))
			req.Host = tt.host
			req.Header.Set("Content-Type", "text/plain")
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tt.want {
				t.Fatalf("status %d, want %d", w.Code, tt.want)
			}
		})
	}
}

func TestWSOrigin(t *testing.T) {
	tests := []struct {
		name   string
		origin string // "SAME" means the test server's own origin
		dev    bool
		want   bool
	}{
		{"absent", "", false, true},
		{"same host", "SAME", false, true},
		{"cross-site", "https://evil.example.test", false, false},
		{"null", "null", false, false},
		{"dev origin in dev mode", devOrigin, true, true},
		{"dev origin outside dev mode", devOrigin, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newGuardServer(tt.dev)
			s.hub = NewHub(s.logger)
			s.setupRoutes()
			go s.hub.Run()
			srv := httptest.NewServer(s.Handler())
			defer srv.Close()
			hdr := http.Header{}
			origin := tt.origin
			if origin == "SAME" {
				origin = srv.URL
			}
			if origin != "" {
				hdr.Set("Origin", origin)
			}
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", hdr)
			if conn != nil {
				conn.Close()
			}
			if (err == nil) != tt.want {
				t.Fatalf("dial err=%v, want accepted=%v", err, tt.want)
			}
		})
	}
}

func TestUpdateConfigPreservesAllowedHosts(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := config.Default()
	cfg.Server.AllowedHosts = []string{"agmux.example.test"}
	cfg.RemoteBrowser.DebugPort = 9555
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}

	s := newGuardServer(false)
	s.setupRoutes()
	// A request that tries to set allowed hosts must not change them.
	body := `{"server":{"port":4321,"allowedHosts":["evil.example.test"],"allowed_hosts":["evil.example.test"]},"daemon":{"interval":"30s"},"session":{"claudeCommand":"claude"},"claude":{"permissionMode":"auto"},"templates":[],"promptTemplates":[]}`
	req := httptest.NewRequest(http.MethodPut, "/api/config", strings.NewReader(body))
	req.Host = "localhost:4321"
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PUT /api/config: %d %s", w.Code, w.Body.String())
	}
	got, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Server.AllowedHosts) != 1 || got.Server.AllowedHosts[0] != "agmux.example.test" {
		t.Errorf("allowed_hosts = %v, want preserved", got.Server.AllowedHosts)
	}
	if got.RemoteBrowser.DebugPort != 9555 {
		t.Errorf("remote_browser.debug_port = %d, want preserved", got.RemoteBrowser.DebugPort)
	}
	if got.Claude.PermissionMode != "auto" {
		t.Errorf("permission_mode not updated: %q", got.Claude.PermissionMode)
	}
}
