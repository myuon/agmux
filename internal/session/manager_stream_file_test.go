package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/myuon/agmux/internal/db"
)

// writeStreamFixture writes a JSONL stream file for the given session ID into
// db.StreamsDir() (HOME must already point at a temp dir) and returns its path.
func writeStreamFixture(t *testing.T, sessionID, content string) {
	t.Helper()
	streamsDir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(streamsDir, sessionID+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const streamFixtureWithTransients = `{"type":"system","subtype":"init","session_id":"chat-1","model":"sonnet-4"}
{"type":"system","subtype":"thinking_tokens","estimated_tokens":100,"session_id":"chat-1"}
{"type":"system","subtype":"thinking_tokens","estimated_tokens":250,"session_id":"chat-1"}
{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"par"}},"session_id":"chat-1"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"answer"}]},"session_id":"chat-1"}
{"type":"system","subtype":"thinking_tokens","estimated_tokens":400,"session_id":"chat-1"}
{"type":"result","subtype":"success","is_error":false,"result":"ok","session_id":"chat-1"}
`

// TestReadStreamFile_FiltersTransientLines verifies the fallback history path
// (used when no live HolderStreamProcess exists, e.g. after the holder exited)
// applies the same transient-line filter as the live path so thinking_tokens
// and stream_event lines never reach the frontend.
//
// Regression for PR #679 review comment 4886401099 (issue 1).
func TestReadStreamFile_FiltersTransientLines(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	sessionID := "test-fallback-filter"
	writeStreamFixture(t, sessionID, streamFixtureWithTransients)

	m := &Manager{}
	lines, err := m.readStreamFile(sessionID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(lines) != 3 {
		t.Fatalf("expected 3 non-transient lines, got %d: %v", len(lines), lines)
	}
	for _, l := range lines {
		if strings.Contains(l, "thinking_tokens") {
			t.Errorf("thinking_tokens line leaked through fallback read: %s", l)
		}
		if strings.Contains(l, "stream_event") {
			t.Errorf("stream_event line leaked through fallback read: %s", l)
		}
	}

	// The limit must apply to the filtered lines, not the raw file lines.
	limited, err := m.readStreamFile(sessionID, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Fatalf("expected 2 lines with limit=2, got %d: %v", len(limited), limited)
	}
	if !strings.Contains(limited[0], `"type":"assistant"`) || !strings.Contains(limited[1], `"type":"result"`) {
		t.Errorf("expected last 2 filtered lines (assistant, result), got %v", limited)
	}
}

// TestReadStreamFileAfter_FiltersTransientLines verifies the delta fallback
// path filters transient lines and counts the `after` cursor over filtered
// lines (matching the live path's sp.lines indexing).
func TestReadStreamFileAfter_FiltersTransientLines(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	sessionID := "test-fallback-filter-after"
	writeStreamFixture(t, sessionID, streamFixtureWithTransients)

	m := &Manager{}

	// after=0 returns all filtered lines
	all, err := m.readStreamFileAfter(sessionID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 non-transient lines, got %d: %v", len(all), all)
	}

	// after=1 skips the filtered init line and returns assistant + result;
	// transient lines must not consume cursor positions.
	rest, err := m.readStreamFileAfter(sessionID, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest) != 2 {
		t.Fatalf("expected 2 lines after cursor 1, got %d: %v", len(rest), rest)
	}
	if !strings.Contains(rest[0], `"type":"assistant"`) || !strings.Contains(rest[1], `"type":"result"`) {
		t.Errorf("expected (assistant, result) after cursor 1, got %v", rest)
	}
	for _, l := range rest {
		if strings.Contains(l, "thinking_tokens") || strings.Contains(l, "stream_event") {
			t.Errorf("transient line leaked through delta fallback read: %s", l)
		}
	}
}
