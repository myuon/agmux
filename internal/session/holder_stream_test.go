package session

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/db"
)

// TestLoadExistingLines_CursorThinkingBufferReset verifies the fix for the
// post-replay buffer leak: when loadExistingLines replays a JSONL file whose
// last turn never reached `thinking/completed` (e.g. the CLI process died
// mid-turn), the cursor provider's per-session thinking buffer must be reset
// at the end of replay so that the next live event does not get prefixed with
// stale thinking text from a previous turn.
//
// Regression for PR #651 review comment 4526884382.
func TestLoadExistingLines_CursorThinkingBufferReset(t *testing.T) {
	// loadExistingLines reads from db.StreamsDir(), so point HOME at a temp
	// dir to isolate file IO.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	streamsDir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "test-cursor-buffer-reset"
	jsonlPath := filepath.Join(streamsDir, sessionID+".jsonl")

	// Simulate a JSONL where the last turn emitted thinking/delta events but
	// never reached thinking/completed (e.g. the process died mid-turn).
	// Two complete turns precede it so we exercise normal replay too.
	content := `{"type":"system","subtype":"init","session_id":"chat-1","model":"sonnet-4"}
{"type":"thinking","subtype":"delta","text":"turn1 ","session_id":"chat-1"}
{"type":"thinking","subtype":"delta","text":"thought","session_id":"chat-1"}
{"type":"thinking","subtype":"completed","session_id":"chat-1"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"answer1"}]},"session_id":"chat-1"}
{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"chat-1"}
{"type":"thinking","subtype":"delta","text":"leftover-","session_id":"chat-1"}
{"type":"thinking","subtype":"delta","text":"never-flushed","session_id":"chat-1"}
`
	if err := os.WriteFile(jsonlPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	provider := NewCursorProvider("")
	sp := &HolderStreamProcess{
		provider: provider,
	}

	sp.loadExistingLines(sessionID, 0)

	// After replay, the buffer for the cursor session must be empty: the
	// trailing un-flushed delta should have been dropped by ResetBuffers,
	// NOT carried over into live processing.
	provider.thinkingMu.Lock()
	_, hasBuf := provider.thinkingBuffers["chat-1"]
	totalBuffers := len(provider.thinkingBuffers)
	provider.thinkingMu.Unlock()

	if hasBuf {
		t.Errorf("expected thinking buffer for chat-1 to be cleared after loadExistingLines, but it still exists")
	}
	if totalBuffers != 0 {
		t.Errorf("expected no thinking buffers after loadExistingLines, got %d", totalBuffers)
	}

	// Now feed a live event for the same session — it must NOT be prefixed
	// with the stale "leftover-never-flushed" thinking text.
	live := provider.NormalizeStreamLine([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"live-answer"}]},"session_id":"chat-1"}`))
	if len(live) != 1 {
		t.Fatalf("expected live event to produce exactly 1 output line (no stale thinking flush), got %d", len(live))
	}
	if strings.Contains(string(live[0]), "leftover") || strings.Contains(string(live[0]), "never-flushed") {
		t.Errorf("live event leaked stale buffered thinking text: %s", string(live[0]))
	}

	// The first replayed turn's thinking should have been preserved during
	// replay (it had completed), so sp.lines should contain at least one
	// thinking message.
	foundReplayedThinking := false
	for _, l := range sp.lines {
		if strings.Contains(l, `"thinking":"turn1 thought"`) {
			foundReplayedThinking = true
			break
		}
	}
	if !foundReplayedThinking {
		t.Errorf("expected replayed thinking text 'turn1 thought' to be in sp.lines, lines=%v", sp.lines)
	}

	// And the trailing un-flushed thinking must NOT appear in sp.lines.
	for _, l := range sp.lines {
		if strings.Contains(l, "leftover") || strings.Contains(l, "never-flushed") {
			t.Errorf("un-flushed trailing thinking leaked into replayed lines: %s", l)
		}
	}
}

// TestLoadExistingLines_ClaudeProviderResetBuffers_NoOp ensures that calling
// loadExistingLines with a non-cursor provider does not panic and the no-op
// ResetBuffers is invoked cleanly.
func TestLoadExistingLines_ClaudeProviderResetBuffers_NoOp(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	streamsDir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "test-claude-reset-noop"
	jsonlPath := filepath.Join(streamsDir, sessionID+".jsonl")

	content := `{"type":"system","subtype":"init","session_id":"c-1"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}
`
	if err := os.WriteFile(jsonlPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	sp := &HolderStreamProcess{
		provider: NewClaudeProvider("", ""),
	}

	// Should not panic.
	sp.loadExistingLines(sessionID, 0)

	if len(sp.lines) == 0 {
		t.Errorf("expected replayed lines, got 0")
	}
}

// TestLoadExistingLines_ThinkingTokensNotPersisted verifies that
// system/thinking_tokens progress events (emitted by the claude provider for
// models with redacted thinking, 100+ lines per session) are treated as
// transient during replay: they must NOT be loaded into sp.lines, while
// surrounding non-transient lines are preserved.
//
// Regression for issue #675.
func TestLoadExistingLines_ThinkingTokensNotPersisted(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	streamsDir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "test-thinking-tokens"
	jsonlPath := filepath.Join(streamsDir, sessionID+".jsonl")

	content := `{"type":"system","subtype":"init","session_id":"c-1"}
{"type":"system","subtype":"thinking_tokens","estimated_tokens":50,"estimated_tokens_delta":50,"session_id":"c-1"}
{"type":"system","subtype":"thinking_tokens","estimated_tokens":128,"estimated_tokens_delta":78,"session_id":"c-1"}
{"type":"system","subtype":"thinking_tokens","estimated_tokens":250,"estimated_tokens_delta":122,"session_id":"c-1"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"c-1"}
`
	if err := os.WriteFile(jsonlPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	sp := &HolderStreamProcess{
		provider: NewClaudeProvider("", ""),
	}

	sp.loadExistingLines(sessionID, 0)

	if len(sp.lines) != 3 {
		t.Errorf("expected 3 replayed lines (init, assistant, result), got %d: %v", len(sp.lines), sp.lines)
	}
	for _, l := range sp.lines {
		if strings.Contains(l, "thinking_tokens") {
			t.Errorf("thinking_tokens event leaked into replayed history: %s", l)
		}
	}
}

// TestIsTransientStreamLine covers the transient-line classification used by
// both replay and live processing.
func TestIsTransientStreamLine(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"stream_event", `{"type":"stream_event","event":{"type":"content_block_delta"}}`, true},
		{"thinking_tokens", `{"type":"system","subtype":"thinking_tokens","estimated_tokens":50}`, true},
		{"system_init", `{"type":"system","subtype":"init","session_id":"c-1"}`, false},
		{"assistant", `{"type":"assistant","message":{"content":[]}}`, false},
		{"result", `{"type":"result","subtype":"success"}`, false},
		{"non_json", `plain text`, false},
		{"empty", ``, false},
		{"invalid_json", `{broken`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTransientStreamLine([]byte(tc.line)); got != tc.want {
				t.Errorf("isTransientStreamLine(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

// TestReadLoop_EOFTriggersOnProcessExit verifies the follow-up fix: when the
// holder socket reaches EOF (= the holder process died unexpectedly without
// sending a control "exited" event), the readLoop must fire the
// onProcessExit callback so the daemon can update session status. Without
// this, the status stays stuck on "running" until manual intervention.
func TestReadLoop_EOFTriggersOnProcessExit(t *testing.T) {
	// Set up a pair of connected unix sockets via net.Pipe-like construct
	// using socketpair via listen+dial. Use shortTempDir to stay under the
	// macOS 104-byte unix socket path limit.
	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "rl-eof.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	serverDone := make(chan net.Conn, 1)
	go func() {
		c, err := l.Accept()
		if err != nil {
			serverDone <- nil
			return
		}
		serverDone <- c
	}()

	clientConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	serverConn := <-serverDone
	if serverConn == nil {
		t.Fatal("accept failed")
	}

	const sessionID = "eof-fires-callback"

	type capture struct {
		sessionID string
		err       error
	}
	cbCh := make(chan capture, 1)

	sp := &HolderStreamProcess{
		conn:       clientConn,
		done:       make(chan struct{}),
		provider:   NewClaudeProvider("", ""),
		holderPID:  12345,
		streamOpts: StreamOpts{SessionID: sessionID},
		onProcessExit: func(sid string, exitErr error) {
			cbCh <- capture{sessionID: sid, err: exitErr}
		},
	}

	go sp.readLoop()

	// Simulate the holder going away unexpectedly: close the server side of
	// the conn, causing EOF on the client side.
	_ = serverConn.Close()

	var got capture
	select {
	case got = <-cbCh:
	case <-time.After(3 * time.Second):
		t.Fatalf("onProcessExit did not fire within 3s after EOF")
	}

	// Wait for readLoop to fully exit.
	select {
	case <-sp.done:
	case <-time.After(3 * time.Second):
		t.Fatalf("readLoop did not exit within 3s after callback fired")
	}

	if got.sessionID != sessionID {
		t.Errorf("callback got sessionID %q, want %q", got.sessionID, sessionID)
	}
	if got.err == nil {
		t.Errorf("callback got nil error, want a non-nil EOF-related error")
	} else if !strings.Contains(got.err.Error(), "EOF") && !strings.Contains(got.err.Error(), "closed") {
		t.Errorf("callback error %q should reference EOF / closed conn", got.err.Error())
	}
}

// TestReadLoop_StoppedSuppressesOnProcessExitOnEOF verifies that when Stop /
// sendCodex has marked sp.stopped = true, an EOF on the conn does NOT
// re-fire onProcessExit. This preserves the existing intentional-shutdown
// flow where the callback either was (Stop path) or will be (control
// "exited" path) handled separately.
func TestReadLoop_StoppedSuppressesOnProcessExitOnEOF(t *testing.T) {
	dir := shortTempDir(t)
	sockPath := filepath.Join(dir, "rl-stop.sock")
	l, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer l.Close()

	serverDone := make(chan net.Conn, 1)
	go func() {
		c, _ := l.Accept()
		serverDone <- c
	}()
	clientConn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	serverConn := <-serverDone
	if serverConn == nil {
		t.Fatal("accept failed")
	}

	var fired atomic.Bool
	sp := &HolderStreamProcess{
		conn:       clientConn,
		done:       make(chan struct{}),
		provider:   NewClaudeProvider("", ""),
		streamOpts: StreamOpts{SessionID: "stopped-eof"},
		stopped:    true, // explicit shutdown in progress
		onProcessExit: func(sid string, exitErr error) {
			fired.Store(true)
		},
	}

	go sp.readLoop()

	_ = serverConn.Close()

	select {
	case <-sp.done:
	case <-time.After(3 * time.Second):
		t.Fatalf("readLoop did not exit within 3s after EOF")
	}

	if fired.Load() {
		t.Errorf("onProcessExit should not fire when sp.stopped=true, but it did")
	}
}



