package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type loginHookRecorder struct {
	mu    sync.Mutex
	calls []string
	ch    chan struct{}
}

func newLoginHookRecorder() *loginHookRecorder {
	return &loginHookRecorder{ch: make(chan struct{}, 8)}
}

func (r *loginHookRecorder) hook(sessionID, reason string) {
	r.mu.Lock()
	r.calls = append(r.calls, sessionID+":"+reason)
	r.mu.Unlock()
	r.ch <- struct{}{}
}

func (r *loginHookRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func TestClaudeLoginHook_FiresOnErrorResultOnly(t *testing.T) {
	m, db := newSwitchModelTestManager(t)
	id := "login-hook"
	insertSwitchModelSession(t, db, id, "claude", "working", "", 0)
	rec := newLoginHookRecorder()
	m.SetOnClaudeLoginRequired(rec.hook)
	fh := attachFakeHolder(t, m, id, StreamOpts{})

	// The marker in a user message, an assistant message and a successful
	// result (the agent's own answer) must not trigger.
	lines := []string{
		`{"type":"user","message":{"role":"user","content":"Please run /login って出たらどうする？"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Not logged in · Please run /login"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"claude auth login を実行してください"}`,
	}
	for _, l := range lines {
		if _, err := fh.server.Write([]byte(l + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	// The CLI's own error result triggers.
	if _, err := fh.server.Write([]byte(`{"type":"result","subtype":"success","is_error":true,"result":"Not logged in · Please run /login"}` + "\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rec.ch:
	case <-time.After(3 * time.Second):
		t.Fatal("hook did not fire for the error result")
	}
	if got := rec.count(); got != 1 {
		t.Fatalf("hook calls = %d, want 1 (%v)", got, rec.calls)
	}
	if rec.calls[0] != id+":Not logged in · Please run /login" {
		t.Errorf("call = %q", rec.calls[0])
	}
}

func TestClaudeLoginLostAtExit_HolderLog(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	id := "exit-log"
	path := HolderLogPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(s string) {
		if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if ok, _ := claudeLoginLostAtExit(id, "exit code 1: process exited with code 1"); ok {
		t.Error("no log: must not trigger")
	}
	write("2026/11/18 10:00:00 INFO holder: CLI process started pid=1 sessionId=exit-log\nError: Not logged in · Please run /login\n")
	if ok, _ := claudeLoginLostAtExit(id, "exit code 1: process exited with code 1"); !ok {
		t.Error("CLI stderr in the holder log must trigger")
	}
	write("Error: Not logged in · Please run /login\n2026/11/18 11:00:00 INFO holder: CLI process started pid=2 sessionId=exit-log\nsegfault\n")
	if ok, _ := claudeLoginLostAtExit(id, "exit code 1: process exited with code 1"); ok {
		t.Error("stderr of an older CLI run must not trigger")
	}
	if ok, _ := claudeLoginLostAtExit(id, "exit code 1: OAuth token has expired"); !ok {
		t.Error("exit message must trigger")
	}
}
