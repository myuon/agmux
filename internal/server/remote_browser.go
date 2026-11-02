package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"github.com/myuon/agmux/internal/browser"
)

// RemoteBrowser is the subset of browser.Manager the HTTP layer needs.
type RemoteBrowser interface {
	Start(ctx context.Context) (browser.Status, error)
	Status(ctx context.Context) browser.Status
	Stop(ctx context.Context) (browser.Status, browser.StopResult, error)
	UserTab(ctx context.Context) (*browser.UserTab, error)
	AgentTab(ctx context.Context, key string) (browser.AgentTab, error)
	CloseAgentTab(ctx context.Context, key string) error
	ServeAgentCDP(ctx context.Context, key string, client *websocket.Conn) error
	Shutdown(ctx context.Context)
}

// SetRemoteBrowser wires the remote browser manager (nil leaves it disabled).
func (s *Server) SetRemoteBrowser(rb RemoteBrowser) {
	s.remoteBrowser = rb
	if rb == nil {
		return
	}
	// A session's dedicated tab is closed when the session ends.
	s.sessions.SetOnSessionEnd(func(sessionID string) {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := rb.CloseAgentTab(ctx, sessionID); err != nil {
				s.logger.Warn("close agent tab failed", "sessionId", sessionID, "error", err)
			}
		}()
	})
}

// ShutdownRemoteBrowser gracefully closes Chrome on server shutdown.
func (s *Server) ShutdownRemoteBrowser(ctx context.Context) {
	if s.remoteBrowser != nil {
		s.remoteBrowser.Shutdown(ctx)
	}
}

// isLoopbackRemote reports whether the actual TCP peer is a loopback address.
// Only r.RemoteAddr is consulted: forwarding headers (X-Forwarded-For,
// CF-Connecting-IP, ...) are attacker-controlled and deliberately ignored.
// cloudflared runs on this host and connects via localhost, so tunnelled
// traffic is loopback while direct LAN traffic is not.
func isLoopbackRemote(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// checkRemoteBrowserWSOrigin is the WebSocket CheckOrigin: the Origin header
// must be present and match the request Host (or the dev origin).
func (s *Server) checkRemoteBrowserWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	return s.originAllowed(r, origin)
}

// remoteBrowserGuard rejects non-loopback peers and, for state-changing
// methods, cross-site Origins. A missing Origin is allowed on POST (CLI/curl).
func (s *Server) remoteBrowserGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackRemote(r) {
			writeError(w, http.StatusForbidden, "remote browser is only available via localhost")
			return
		}
		if isStateChanging(r.Method) {
			if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(r, origin) {
				writeError(w, http.StatusForbidden, "cross-site request rejected")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) mountRemoteBrowser(r chi.Router) {
	r.Route("/remote-browser", func(r chi.Router) {
		r.Use(s.remoteBrowserGuard)
		r.Get("/status", s.remoteBrowserStatus)
		r.Post("/start", s.remoteBrowserStart)
		r.Post("/stop", s.remoteBrowserStop)
		r.Post("/agent-tabs/{sessionID}", s.remoteBrowserAgentTab)
		r.Get("/agent-cdp/{sessionID}", s.remoteBrowserAgentCDP)
		r.Get("/ws", s.remoteBrowserWS)
	})
}

func (s *Server) remoteBrowserReady(w http.ResponseWriter) bool {
	if s.remoteBrowser == nil {
		writeError(w, http.StatusServiceUnavailable, "remote browser is not configured")
		return false
	}
	return true
}

func (s *Server) remoteBrowserStatus(w http.ResponseWriter, r *http.Request) {
	if !s.remoteBrowserReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.remoteBrowser.Status(r.Context()))
}

func (s *Server) remoteBrowserStart(w http.ResponseWriter, r *http.Request) {
	if !s.remoteBrowserReady(w) {
		return
	}
	st, err := s.remoteBrowser.Start(r.Context())
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, browser.ErrChromeNotFound) {
			code = http.StatusUnprocessableEntity
		}
		s.logger.Warn("remote browser start failed", "error", err)
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) remoteBrowserStop(w http.ResponseWriter, r *http.Request) {
	if !s.remoteBrowserReady(w) {
		return
	}
	st, res, err := s.remoteBrowser.Stop(r.Context())
	if err != nil {
		s.logger.Warn("remote browser stop failed", "error", err)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": st, "graceful": res.Graceful})
}

// remoteBrowserAgentTab allocates (or returns) the tab dedicated to one agmux
// session, starting Chrome if needed. Called by the MCP tool.
func (s *Server) remoteBrowserAgentTab(w http.ResponseWriter, r *http.Request) {
	if !s.remoteBrowserReady(w) {
		return
	}
	id := chi.URLParam(r, "sessionID")
	if _, err := s.sessions.Get(id); err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	tab, err := s.remoteBrowser.AgentTab(r.Context(), id)
	if err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, browser.ErrChromeNotFound) {
			code = http.StatusUnprocessableEntity
		}
		s.logger.Warn("remote browser agent tab failed", "sessionId", id, "error", err)
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"targetId": tab.TargetID,
		"reused":   tab.Reused,
		// Per-session CDP endpoint that only exposes this session's tabs.
		"cdpUrl": fmt.Sprintf("ws://127.0.0.1:%d/api/remote-browser/agent-cdp/%s", s.port, id),
	})
}

// remoteBrowserAgentCDP is the filtered DevTools endpoint agent-browser
// connects to (--cdp). It exposes only the tabs owned by the session.
func (s *Server) remoteBrowserAgentCDP(w http.ResponseWriter, r *http.Request) {
	if !s.remoteBrowserReady(w) {
		return
	}
	id := chi.URLParam(r, "sessionID")
	if _, err := s.sessions.Get(id); err != nil {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	up := websocket.Upgrader{
		// CLI clients send no Origin; a browser page (any Origin that is not
		// same-site) must not reach the logged-in Chrome through here.
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			return origin == "" || s.originAllowed(r, origin)
		},
		ReadBufferSize:  32 * 1024,
		WriteBufferSize: 32 * 1024,
	}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	if err := s.remoteBrowser.ServeAgentCDP(r.Context(), id, conn); err != nil &&
		!websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
		s.logger.Debug("agent cdp closed", "sessionId", id, "error", err.Error())
	}
}

// remoteBrowserWS relays screencast frames to the client and client input to
// the user tab. It uses its own upgrader with strict Origin validation; the
// permissive upgrader in ws.go is intentionally not reused.
func (s *Server) remoteBrowserWS(w http.ResponseWriter, r *http.Request) {
	if !s.remoteBrowserReady(w) {
		return
	}
	// Origin is checked by the upgrader, but fail early with a clear status
	// before touching Chrome.
	if !s.checkRemoteBrowserWSOrigin(r) {
		writeError(w, http.StatusForbidden, "origin not allowed")
		return
	}
	if !s.remoteBrowser.Status(r.Context()).Running {
		writeError(w, http.StatusConflict, "remote browser is not running")
		return
	}
	tab, err := s.remoteBrowser.UserTab(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	up := websocket.Upgrader{
		CheckOrigin:     s.checkRemoteBrowserWSOrigin,
		ReadBufferSize:  4096,
		WriteBufferSize: 256 * 1024,
	}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote the response
	}
	defer conn.Close()

	const (
		pongWait   = 60 * time.Second
		pingPeriod = 25 * time.Second
	)
	conn.SetReadLimit(64 << 10)
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	sub, err := tab.Subscribe(r.Context())
	if err != nil {
		conn.WriteJSON(map[string]string{"type": "error", "message": err.Error()})
		return
	}
	defer sub.Close()

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			conn.SetReadDeadline(time.Now().Add(pongWait))
			var in browser.InputMessage
			if json.Unmarshal(data, &in) != nil {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = tab.HandleInput(ctx, in)
			cancel()
			if err != nil {
				// Log only the message type: never typed text, keys or URLs.
				s.logger.Debug("remote browser input failed", "type", in.Type)
			}
		}
	}()

	ping := time.NewTicker(pingPeriod)
	defer ping.Stop()
	for {
		select {
		case msg, ok := <-sub.C:
			if !ok {
				return
			}
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ping.C:
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-readDone:
			return
		}
	}
}
