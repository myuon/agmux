package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/claudeauth"
	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/secret"
	"github.com/myuon/agmux/internal/session"
)

// Built from parts so the fixture does not look like a real token to secret
// scanners.
var fakeClaudeToken = "sk-ant-" + "oat01-" + "serverTEST_token-0123456789abcdefghijklmn"

const fakeTokenURL = "https://claude.com/cai/oauth/authorize?code=true&scope=user%3Ainference&state=tokstate"

// setupTokenStub emulates `claude setup-token`: it shows the URL and, once a
// code and Enter were typed, prints the token and waits (like the CLI).
type setupTokenStub struct {
	r      *io.PipeReader
	w      *io.PipeWriter
	mu     sync.Mutex
	typed  string
	once   sync.Once
	exited chan struct{}
	token  string
}

func newSetupTokenStub(token string) *setupTokenStub {
	r, w := io.Pipe()
	return &setupTokenStub{r: r, w: w, exited: make(chan struct{}), token: token}
}
func (p *setupTokenStub) Read(b []byte) (int, error) { return p.r.Read(b) }
func (p *setupTokenStub) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.typed += string(b)
	p.mu.Unlock()
	if string(b) == "\r" {
		go func() {
			_, _ = p.w.Write([]byte("Long-lived authentication token created successfully!\r\n\r\nYour OAuth token (valid for 1 year):\r\n\r\n" +
				p.token + "\r\n\r\nStore this token securely. You won't be able to see it again.\r\n"))
		}()
	}
	return len(b), nil
}
func (p *setupTokenStub) Wait() error { <-p.exited; return nil }
func (p *setupTokenStub) Kill() error {
	p.once.Do(func() { _ = p.w.Close(); close(p.exited) })
	return nil
}

type tokenTestEnv struct {
	s        *Server
	logs     *bytes.Buffer
	keychain *memBackend
	mon      *claudeauth.Monitor
	stub     *setupTokenStub
}

func newClaudeTokenTestServer(t *testing.T) *tokenTestEnv {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	kc := &memBackend{kind: secret.BackendKeychain, items: map[string]string{}}
	s := &Server{
		logger: logger, hub: NewHub(logger), sessions: &authSessions{}, sqlDB: sqlDB, startTime: time.Now(),
		secrets: secret.NewStore(sqlDB),
		secretBackends: secret.Registry{
			secret.BackendKeychain: kc,
			secret.BackendFile:     &memBackend{kind: secret.BackendFile, items: map[string]string{}},
		},
	}
	s.setupRoutes()
	env := &tokenTestEnv{s: s, logs: logs, keychain: kc}
	env.mon = s.initClaudeAuth(
		func() (claudeauth.Process, error) { return nil, errors.New("auth login is not used here") },
		func(context.Context) (bool, error) { return true, nil },
		func() (claudeauth.Process, error) {
			env.stub = newSetupTokenStub(fakeClaudeToken)
			go func() {
				_, _ = env.stub.w.Write([]byte("\x1b[2GBrowser\x1b[10Gdidn't\x1b[17Gopen?\r\n\r\n\x1b]8;id=1;" + fakeTokenURL + "\x07" + fakeTokenURL + "\x1b]8;;\x07\r\n\r\n\x1b[2GPaste\x1b[8Gcode\x1b[13Ghere\r\n"))
			}()
			return env.stub, nil
		},
	)
	s.claudeAuth.token.SubmitDelay = time.Millisecond
	return env
}

func waitTokenPhase(t *testing.T, s *Server, want claudeauth.LoginPhase) claudeAuthStatusResponse {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var st claudeAuthStatusResponse
	for time.Now().Before(deadline) {
		st = decodeAuthStatus(t, doJSON(t, s, http.MethodGet, "/api/claude-auth/status", "").Body.Bytes())
		if st.Token.Flow.Phase == want {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("token phase did not become %s: %+v", want, st.Token.Flow)
	return st
}

func TestClaudeTokenDisabled(t *testing.T) {
	env := newClaudeTokenTestServer(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/api/claude-auth/token/start"},
		{http.MethodPost, "/api/claude-auth/token/code"},
		{http.MethodPost, "/api/claude-auth/token/cancel"},
		{http.MethodDelete, "/api/claude-auth/token"},
	} {
		if rec := doJSON(t, env.s, c.method, c.path, `{}`); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 while claude_relogin is off", c.method, c.path, rec.Code)
		}
	}
	// A stored token is not passed to sessions while the feature is off.
	env.keychain.items[claudeauth.TokenSecretName] = fakeClaudeToken
	if _, _, err := env.s.secrets.Upsert(secret.Secret{Name: claudeauth.TokenSecretName, Backend: secret.BackendKeychain}); err != nil {
		t.Fatal(err)
	}
	if got := env.s.ClaudeSessionEnv(); got != nil {
		t.Errorf("env while off = %v, want nil", got)
	}
	// Nor is anyone notified about its expiry.
	env.s.claudeAuth.now = func() time.Time { return time.Now().Add(400 * 24 * time.Hour) }
	env.s.checkClaudeTokenExpiry()
	if n := countTokenNotices(t, env.s); n != 0 {
		t.Errorf("notifications while off = %d", n)
	}
}

func TestClaudeTokenCreateFlow(t *testing.T) {
	env := newClaudeTokenTestServer(t)
	s := env.s
	if err := experimental.Set(claudeauth.FeatureName, true); err != nil {
		t.Fatal(err)
	}
	if got := s.ClaudeSessionEnv(); got != nil {
		t.Errorf("env without a token = %v", got)
	}
	st := decodeAuthStatus(t, doJSON(t, s, http.MethodGet, "/api/claude-auth/status", "").Body.Bytes())
	if st.Token.Present || st.Token.Flow.Phase != claudeauth.PhaseIdle {
		t.Fatalf("initial token = %+v", st.Token)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/claude-auth/token/start", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	var ls claudeauth.LoginStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &ls)
	if ls.Phase != claudeauth.PhaseWaitingCode || ls.URL != fakeTokenURL {
		t.Fatalf("start status = %+v", ls)
	}
	if rec := doJSON(t, s, http.MethodPost, "/api/claude-auth/token/code", `{"code":"thecode#tokstate"}`); rec.Code != http.StatusOK {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	before := time.Now().Add(-time.Second)
	st = waitTokenPhase(t, s, claudeauth.PhaseSucceeded)

	env.stub.mu.Lock()
	typed := env.stub.typed
	env.stub.mu.Unlock()
	if typed != "thecode#tokstate\r" {
		t.Errorf("typed = %q", typed)
	}
	if env.keychain.items[claudeauth.TokenSecretName] != fakeClaudeToken {
		t.Errorf("stored value = %q", env.keychain.items[claudeauth.TokenSecretName])
	}
	if !st.Token.Present || st.Token.CreatedAt == nil || st.Token.CreatedAt.Before(before) || st.Token.ExpiringSoon {
		t.Fatalf("token after create = %+v", st.Token)
	}
	if want := st.Token.CreatedAt.Add(365 * 24 * time.Hour); !st.Token.ExpiresAt.Equal(want) {
		t.Errorf("expiresAt = %v, want %v", st.Token.ExpiresAt, want)
	}

	// The token is never in a response or the log.
	for _, body := range []string{
		doJSON(t, s, http.MethodGet, "/api/claude-auth/status", "").Body.String(),
		doJSON(t, s, http.MethodGet, "/api/secrets", "").Body.String(),
		doJSON(t, s, http.MethodGet, "/api/secrets/"+claudeauth.TokenSecretName, "").Body.String(),
		env.logs.String(),
	} {
		if strings.Contains(body, fakeClaudeToken) || strings.Contains(body, "serverTEST") {
			t.Errorf("token leaked: %s", body)
		}
	}
	if strings.Contains(env.logs.String(), "thecode") || strings.Contains(env.logs.String(), "tokstate") {
		t.Errorf("code or URL leaked into the log: %s", env.logs.String())
	}

	// Sessions get it as CLAUDE_CODE_OAUTH_TOKEN.
	got := s.ClaudeSessionEnv()
	if got[claudeauth.TokenSecretName] != fakeClaudeToken || len(got) != 1 {
		t.Errorf("session env = %v", got)
	}

	// Re-creating resets the creation time.
	sec, _ := s.secrets.Get(claudeauth.TokenSecretName)
	if _, err := s.sqlDB.Exec(`UPDATE secrets SET created_at = ? WHERE name = ?`,
		time.Now().Add(-360*24*time.Hour).UTC().Format(time.RFC3339Nano), sec.Name); err != nil {
		t.Fatal(err)
	}
	st = decodeAuthStatus(t, doJSON(t, s, http.MethodGet, "/api/claude-auth/status", "").Body.Bytes())
	if !st.Token.ExpiringSoon {
		t.Fatalf("a 360-day-old token must be expiring soon: %+v", st.Token)
	}
	doJSON(t, s, http.MethodPost, "/api/claude-auth/token/start", "")
	doJSON(t, s, http.MethodPost, "/api/claude-auth/token/code", `{"code":"again"}`)
	st = waitTokenPhase(t, s, claudeauth.PhaseSucceeded)
	if st.Token.ExpiringSoon || st.Token.CreatedAt.Before(before) {
		t.Errorf("re-created token = %+v", st.Token)
	}

	// Delete.
	rec = doJSON(t, s, http.MethodDelete, "/api/claude-auth/token", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if decodeAuthStatus(t, rec.Body.Bytes()).Token.Present {
		t.Error("token must be gone")
	}
	if _, ok := env.keychain.items[claudeauth.TokenSecretName]; ok {
		t.Error("value must be deleted")
	}
	if got := s.ClaudeSessionEnv(); got != nil {
		t.Errorf("env after delete = %v", got)
	}
	if rec := doJSON(t, s, http.MethodDelete, "/api/claude-auth/token", ""); rec.Code != http.StatusNotFound {
		t.Errorf("second delete = %d", rec.Code)
	}
}

func TestClaudeTokenUnreadableIsNotSaved(t *testing.T) {
	env := newClaudeTokenTestServer(t)
	s := env.s
	if err := experimental.Set(claudeauth.FeatureName, true); err != nil {
		t.Fatal(err)
	}
	doJSON(t, s, http.MethodPost, "/api/claude-auth/token/start", "")
	env.stub.token = "not-a-token-format"
	doJSON(t, s, http.MethodPost, "/api/claude-auth/token/code", `{"code":"c"}`)
	// The stub keeps running: agmux gives up when it exits.
	time.Sleep(50 * time.Millisecond)
	_ = env.stub.Kill()
	st := waitTokenPhase(t, s, claudeauth.PhaseFailed)
	if st.Token.Flow.Message != claudeauth.TokenUnreadableMessage {
		t.Errorf("message = %q", st.Token.Flow.Message)
	}
	if st.Token.Present || len(env.keychain.items) != 0 {
		t.Errorf("nothing must be saved: %+v %v", st.Token, env.keychain.items)
	}
}

func countTokenNotices(t *testing.T, s *Server) int {
	t.Helper()
	var n int
	if err := s.sqlDB.QueryRow(`SELECT COUNT(*) FROM notifications WHERE message LIKE ?`, claudeTokenNoticePrefix+"%").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestClaudeTokenExpiryNotice(t *testing.T) {
	env := newClaudeTokenTestServer(t)
	s := env.s
	if err := experimental.Set(claudeauth.FeatureName, true); err != nil {
		t.Fatal(err)
	}
	// No token: nothing.
	s.checkClaudeTokenExpiry()
	if n := countTokenNotices(t, s); n != 0 {
		t.Fatalf("notices without a token = %d", n)
	}

	env.keychain.items[claudeauth.TokenSecretName] = fakeClaudeToken
	if _, _, err := s.secrets.Upsert(secret.Secret{Name: claudeauth.TokenSecretName, Backend: secret.BackendKeychain}); err != nil {
		t.Fatal(err)
	}
	sec, _ := s.secrets.Get(claudeauth.TokenSecretName)
	created := sec.CreatedAt

	// 15 days before the expiry: not yet.
	s.claudeAuth.now = func() time.Time { return created.Add(350 * 24 * time.Hour) }
	s.checkClaudeTokenExpiry()
	if n := countTokenNotices(t, s); n != 0 {
		t.Fatalf("notices 15 days before = %d", n)
	}

	// 14 days before: one notice, at most once a day.
	s.claudeAuth.now = func() time.Time { return created.Add(351 * 24 * time.Hour) }
	s.checkClaudeTokenExpiry()
	s.checkClaudeTokenExpiry()
	if n := countTokenNotices(t, s); n != 1 {
		t.Fatalf("notices = %d, want 1", n)
	}
	st := decodeAuthStatus(t, doJSON(t, s, http.MethodGet, "/api/claude-auth/status", "").Body.Bytes())
	if !st.Token.ExpiringSoon || st.Token.Expired {
		t.Errorf("token = %+v", st.Token)
	}

	// The next day it is sent again.
	if _, err := s.sqlDB.Exec(`UPDATE notifications SET created_at = datetime('now', '-25 hours')`); err != nil {
		t.Fatal(err)
	}
	s.checkClaudeTokenExpiry()
	if n := countTokenNotices(t, s); n != 2 {
		t.Fatalf("notices after a day = %d, want 2", n)
	}
	var msg string
	_ = s.sqlDB.QueryRow(`SELECT message FROM notifications ORDER BY id DESC LIMIT 1`).Scan(&msg)
	if !strings.Contains(msg, created.Add(365*24*time.Hour).Local().Format("2006-01-02")) {
		t.Errorf("message = %q", msg)
	}
	if strings.Contains(msg, fakeClaudeToken) {
		t.Error("token leaked into the notification")
	}
}

// The env reaches the claude command line built for every session start.
func TestClaudeSessionEnvReachesCommand(t *testing.T) {
	env := newClaudeTokenTestServer(t)
	if err := experimental.Set(claudeauth.FeatureName, true); err != nil {
		t.Fatal(err)
	}
	env.keychain.items[claudeauth.TokenSecretName] = fakeClaudeToken
	if _, _, err := env.s.secrets.Upsert(secret.Secret{Name: claudeauth.TokenSecretName, Backend: secret.BackendKeychain}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(claudeauth.TokenSecretName, "inherited")
	session.SetClaudeEnvSource(env.s.ClaudeSessionEnv)
	t.Cleanup(func() { session.SetClaudeEnvSource(nil) })

	cmd := session.NewClaudeProvider("claude", "").BuildStreamCommand(session.StreamOpts{SessionID: "s1", ProjectPath: t.TempDir()})
	var vals []string
	for _, kv := range cmd.Env {
		if k, v, _ := strings.Cut(kv, "="); k == claudeauth.TokenSecretName {
			vals = append(vals, v)
		}
	}
	if len(vals) != 1 || vals[0] != fakeClaudeToken {
		t.Errorf("CLAUDE_CODE_OAUTH_TOKEN in env = %v", vals)
	}

	if err := experimental.Set(claudeauth.FeatureName, false); err != nil {
		t.Fatal(err)
	}
	cmd = session.NewClaudeProvider("claude", "").BuildStreamCommand(session.StreamOpts{SessionID: "s1", ProjectPath: t.TempDir()})
	for _, kv := range cmd.Env {
		if k, v, _ := strings.Cut(kv, "="); k == claudeauth.TokenSecretName && v != "inherited" {
			t.Errorf("token passed while off: %s", kv)
		}
	}
}
