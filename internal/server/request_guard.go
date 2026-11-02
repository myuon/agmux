package server

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

// devOrigin is the Vite dev server origin allowed in dev mode.
const devOrigin = "http://localhost:5173"

// SetAllowedHosts sets the extra hostnames accepted in the Host header
// (server.allowed_hosts). Call before serving.
func (s *Server) SetAllowedHosts(hosts []string) {
	m := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			m[h] = true
		}
	}
	s.allowedHosts = m
}

func isStateChanging(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// hostname strips the port and IPv6 brackets from a Host header value and
// lower-cases it.
func hostname(hostport string) string {
	h := hostport
	if sh, _, err := net.SplitHostPort(hostport); err == nil {
		h = sh
	} else if strings.HasPrefix(h, "[") && strings.HasSuffix(h, "]") {
		h = h[1 : len(h)-1]
	}
	return strings.ToLower(h)
}

// hostAllowed reports whether the Host header value is accepted: localhost,
// a literal IP address, or an entry of server.allowed_hosts.
func (s *Server) hostAllowed(hostport string) bool {
	h := hostname(hostport)
	if h == "" {
		return false
	}
	if h == "localhost" || net.ParseIP(h) != nil {
		return true
	}
	return s.allowedHosts[h]
}

// originAllowed reports whether a (present) Origin header is same-site with
// the request Host, or the dev origin in dev mode.
func (s *Server) originAllowed(r *http.Request, origin string) bool {
	if s.devMode && origin == devOrigin {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}

// checkWSOrigin is the CheckOrigin for /ws: an absent Origin is allowed,
// otherwise it must match the request Host (or the dev origin).
func (s *Server) checkWSOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || s.originAllowed(r, origin)
}

// requestGuard validates Host on every request and Origin on state-changing
// requests.
func (s *Server) requestGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			http.Error(w, "forbidden: Host not allowed. Add the hostname to server.allowed_hosts in ~/.agmux/config.toml", http.StatusForbidden)
			return
		}
		if isStateChanging(r.Method) {
			if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(r, origin) {
				http.Error(w, "forbidden: cross-site request rejected", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
