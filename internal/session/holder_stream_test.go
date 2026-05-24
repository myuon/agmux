package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

