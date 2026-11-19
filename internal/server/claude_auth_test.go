package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/claudeauth"
	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/session"
)

// authSessions is a SessionService stub for the re-login handlers.
type authSessions struct {
	session.SessionService
	mu   sync.Mutex
	sent []string
}

func (a *authSessions) Get(id string) (*session.Session, error) {
	return &session.Session{ID: id, Name: "name-" + id, Status: session.StatusIdle}, nil
}
func (a *authSessions) SendKeys(id, text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sent = append(a.sent, id+":"+text)
	return nil
}
func (a *authSessions) UpdateStatus(id string, status session.Status) error { return nil }

// ptyStub emulates `claude auth login` behind the Process interface.
type ptyStub struct {
	r      *io.PipeReader
	w      *io.PipeWriter
	mu     sync.Mutex
	typed  string
	once   sync.Once
	exited chan struct{}
	err    error
}

func newPTYStub() *ptyStub {
	r, w := io.Pipe()
	return &ptyStub{r: r, w: w, exited: make(chan struct{})}
}
func (p *ptyStub) Read(b []byte) (int, error) { return p.r.Read(b) }
func (p *ptyStub) Write(b []byte) (int, error) {
	p.mu.Lock()
	p.typed += string(b)
	p.mu.Unlock()
	// The CLI accepts the code and exits.
	go p.exit(nil)
	return len(b), nil
}
func (p *ptyStub) Wait() error { <-p.exited; return p.err }
func (p *ptyStub) Kill() error  { p.exit(errors.New("killed")); return nil }
func (p *ptyStub) exit(err error) {
	p.once.Do(func() { p.err = err; _ = p.w.Close(); close(p.exited) })
}

func newClaudeAuthTestServer(t *testing.T) (*Server, *authSessions, *claudeauth.Monitor, *ptyStub) {
	t.Helper()
	s, _ := newChoiceTestServer(t) // sets HOME, DB and hub
	sess := &authSessions{}
	s.sessions = sess
	stub := newPTYStub()
	mon := s.initClaudeAuth(
		func() (claudeauth.Process, error) {
			go func() {
				_, _ = stub.w.Write([]byte("If the browser didn't open, visit: https://claude.com/cai/oauth/authorize?code=true&state=s\r\nPaste code here if prompted > "))
			}()
			return stub, nil
		},
		func(context.Context) (bool, error) { return true, nil },
	)
	return s, sess, mon, stub
}

func decodeAuthStatus(t *testing.T, body []byte) claudeAuthStatusResponse {
	t.Helper()
	var st claudeAuthStatusResponse
	if err := json.Unmarshal(body, &st); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return st
}

func TestClaudeAuthDisabled(t *testing.T) {
	s, sess, mon, _ := newClaudeAuthTestServer(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/claude-auth/status"},
		{http.MethodPost, "/api/claude-auth/login/start"},
		{http.MethodPost, "/api/claude-auth/login/code"},
		{http.MethodPost, "/api/claude-auth/login/cancel"},
		{http.MethodPost, "/api/claude-auth/resume"},
		{http.MethodPost, "/api/claude-auth/dismiss"},
	} {
		if rec := doJSON(t, s, c.method, c.path, `{}`); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 while claude_relogin is off", c.method, c.path, rec.Code)
		}
	}
	// Detection is a no-op too.
	s.ReportClaudeLoginRequired("s1", "Not logged in")
	if mon.Required() {
		t.Error("login must not be required while the feature is off")
	}
	if len(sess.sent) != 0 {
		t.Error("nothing must be sent")
	}
}

func TestClaudeAuthReloginFlow(t *testing.T) {
	s, sess, mon, stub := newClaudeAuthTestServer(t)
	if err := experimental.Set(claudeauth.FeatureName, true); err != nil {
		t.Fatal(err)
	}

	st := decodeAuthStatus(t, doJSON(t, s, http.MethodGet, "/api/claude-auth/status", "").Body.Bytes())
	if st.LoginRequired || st.Login.Phase != claudeauth.PhaseIdle {
		t.Fatalf("initial status = %+v", st)
	}

	s.ReportClaudeLoginRequired("s1", "Not logged in · Please run /login")
	st = decodeAuthStatus(t, doJSON(t, s, http.MethodGet, "/api/claude-auth/status", "").Body.Bytes())
	if !st.LoginRequired || len(st.StoppedSessions) != 1 || st.StoppedSessions[0].Name != "name-s1" {
		t.Fatalf("status after report = %+v", st)
	}

	// Resume is refused while still logged out.
	if rec := doJSON(t, s, http.MethodPost, "/api/claude-auth/resume", `{"sessionId":"s1"}`); rec.Code != http.StatusConflict {
		t.Errorf("resume before login = %d, want 409", rec.Code)
	}

	// Code before start: 409.
	if rec := doJSON(t, s, http.MethodPost, "/api/claude-auth/login/code", `{"code":"abc"}`); rec.Code != http.StatusConflict {
		t.Errorf("code before start = %d, want 409", rec.Code)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/claude-auth/login/start", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("start = %d %s", rec.Code, rec.Body)
	}
	var ls claudeauth.LoginStatus
	_ = json.Unmarshal(rec.Body.Bytes(), &ls)
	if ls.Phase != claudeauth.PhaseWaitingCode || !strings.HasPrefix(ls.URL, "https://claude.com/cai/oauth/authorize") {
		t.Fatalf("start status = %+v", ls)
	}
	if rec := doJSON(t, s, http.MethodPost, "/api/claude-auth/login/start", ""); rec.Code != http.StatusConflict {
		t.Errorf("second start = %d, want 409", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodPost, "/api/claude-auth/login/code", `{"code":""}`); rec.Code != http.StatusBadRequest {
		t.Errorf("empty code = %d, want 400", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodPost, "/api/claude-auth/login/code", `{"code":"abc#def"}`); rec.Code != http.StatusOK {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		st = decodeAuthStatus(t, doJSON(t, s, http.MethodGet, "/api/claude-auth/status", "").Body.Bytes())
		if st.Login.Phase == claudeauth.PhaseSucceeded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.Login.Phase != claudeauth.PhaseSucceeded || st.LoginRequired {
		t.Fatalf("after code = %+v", st)
	}
	stub.mu.Lock()
	typed := stub.typed
	stub.mu.Unlock()
	if typed != "abc#def\r" {
		t.Errorf("typed = %q", typed)
	}
	if mon.Required() {
		t.Error("login-required must be cleared")
	}
	if len(st.StoppedSessions) != 1 {
		t.Fatalf("stopped sessions must stay listed for resume: %+v", st.StoppedSessions)
	}

	rec = doJSON(t, s, http.MethodPost, "/api/claude-auth/resume", `{"sessionId":"s1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("resume = %d %s", rec.Code, rec.Body)
	}
	if len(sess.sent) != 1 || !strings.HasPrefix(sess.sent[0], "s1:") {
		t.Errorf("sent = %v", sess.sent)
	}
	if st := decodeAuthStatus(t, rec.Body.Bytes()); len(st.StoppedSessions) != 0 {
		t.Errorf("resumed session must leave the list: %+v", st.StoppedSessions)
	}
	if rec := doJSON(t, s, http.MethodPost, "/api/claude-auth/resume", `{"sessionId":"s1"}`); rec.Code != http.StatusNotFound {
		t.Errorf("second resume = %d, want 404", rec.Code)
	}
}

func TestClaudeAuthDismissAndCancel(t *testing.T) {
	s, _, mon, stub := newClaudeAuthTestServer(t)
	if err := experimental.Set(claudeauth.FeatureName, true); err != nil {
		t.Fatal(err)
	}
	mon.Report("s1", "x")
	mon.Report("s2", "x")
	st := decodeAuthStatus(t, doJSON(t, s, http.MethodPost, "/api/claude-auth/dismiss", `{"sessionId":"s1"}`).Body.Bytes())
	if len(st.StoppedSessions) != 1 || st.StoppedSessions[0].ID != "s2" {
		t.Errorf("after dismiss = %+v", st.StoppedSessions)
	}

	doJSON(t, s, http.MethodPost, "/api/claude-auth/login/start", "")
	var ls claudeauth.LoginStatus
	_ = json.Unmarshal(doJSON(t, s, http.MethodPost, "/api/claude-auth/login/cancel", "").Body.Bytes(), &ls)
	if ls.Phase != claudeauth.PhaseFailed {
		t.Errorf("after cancel = %+v", ls)
	}
	select {
	case <-stub.exited:
	case <-time.After(time.Second):
		t.Error("cancel must kill the login process")
	}
	if !mon.Required() {
		t.Error("cancel keeps the login-required state")
	}
}
