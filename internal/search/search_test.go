package search

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/myuon/agmux/internal/db"
)

func setup(t *testing.T) (*Indexer, string) {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	streams := filepath.Join(dir, "streams")
	if err := os.MkdirAll(streams, 0o755); err != nil {
		t.Fatal(err)
	}
	ix, err := New(sqlDB, streams, func() bool { return true }, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return ix, streams
}

func addSession(t *testing.T, ix *Indexer, id, name, project, provider string) {
	t.Helper()
	if _, err := ix.db.Exec(`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider) VALUES (?, ?, ?, '', 'idle', 'worker', 'stream', ?)`, id, name, project, provider); err != nil {
		t.Fatal(err)
	}
}

func appendLines(t *testing.T, streams, id string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(streams, id+".jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSyncAndSearch(t *testing.T) {
	ix, streams := setup(t)
	addSession(t, ix, "s1", "認証の修正", "/p/a", "claude")
	addSession(t, ix, "s2", "codex作業", "/p/b", "codex")

	appendLines(t, streams, "s1",
		`{"type":"user","message":{"role":"user","content":"Keychain の認証エラーを直して"}}`+"\n",
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"security find 認証"}},{"type":"text","text":"expiresAt=0 が原因でした"}]}}`+"\n",
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"認証 tool output"}]}}`+"\n",
		`{"type":"assistant","message":{"content":[{"type":"text","text":"partial`, // incomplete line
	)
	appendLines(t, streams, "s2",
		`{"type":"item.completed","item":{"id":"i0","type":"agent_message","text":"認証まわりを調べます"}}`+"\n",
	)

	if err := ix.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	res, err := ix.Search(Query{Text: "認証"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("expected 2 hits (user text + codex agent message, not tool io), got %+v", res)
	}

	res, _ = ix.Search(Query{Text: "EXPIRESAT 原因"})
	if len(res) != 1 || res[0].SessionID != "s1" || res[0].SessionName != "認証の修正" {
		t.Fatalf("multi-term case-insensitive search failed: %+v", res)
	}

	res, _ = ix.Search(Query{Text: "認証", ProjectPath: "/p/b"})
	if len(res) != 1 || res[0].SessionID != "s2" {
		t.Fatalf("project filter failed: %+v", res)
	}
	res, _ = ix.Search(Query{Text: "認証", ExcludeSession: "s2"})
	if len(res) != 1 || res[0].SessionID != "s1" {
		t.Fatalf("exclude failed: %+v", res)
	}

	// Completing the partial line indexes it on the next sync, without duplicates.
	appendLines(t, streams, "s1", ` done"}]}}`+"\n")
	if err := ix.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if res, _ = ix.Search(Query{Text: "partial done"}); len(res) != 1 {
		t.Fatalf("expected partial line indexed once: %+v", res)
	}
	if res, _ = ix.Search(Query{Text: "認証"}); len(res) != 2 {
		t.Fatalf("expected no duplicates after resync: %+v", res)
	}

	// Deleted sessions disappear from the index.
	if _, err := ix.db.Exec(`DELETE FROM sessions WHERE id = 's2'`); err != nil {
		t.Fatal(err)
	}
	if err := ix.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	ix.db.QueryRow(`SELECT COUNT(*) FROM search_messages WHERE session_id = 's2'`).Scan(&n)
	if n != 0 {
		t.Fatalf("expected deleted session purged, got %d rows", n)
	}
}

func TestLikeEscaping(t *testing.T) {
	ix, streams := setup(t)
	addSession(t, ix, "s1", "x", "/p", "claude")
	appendLines(t, streams, "s1", `{"type":"user","message":{"content":"100% done"}}`+"\n", `{"type":"user","message":{"content":"100 percent"}}`+"\n")
	ix.Sync(context.Background())
	if res, _ := ix.Search(Query{Text: "100%"}); len(res) != 1 {
		t.Fatalf("%% should be literal: %+v", res)
	}
}

func TestSnippet(t *testing.T) {
	got := Snippet("aaaaaaaaaa KEYWORD bbbbbbbbbb", "keyword", 3)
	if got != "…aa KEYWORD bb…" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractRedacts(t *testing.T) {
	ms := ExtractMessages([]byte(`{"type":"user","message":{"content":"key sk-ant-api03-` + "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH" + `"}}`))
	if len(ms) != 1 || contains(ms[0].Text, "abcdefghijklmnop") {
		t.Fatalf("expected redaction: %+v", ms)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func TestExtractSkipsInjected(t *testing.T) {
	for _, l := range []string{
		`{"type":"user","message":{"content":[{"type":"text","text":"Base directory for this skill: /x\n# Skill"}]}}`,
		`{"type":"user","message":{"content":"<system-reminder>\nfoo\n</system-reminder>"}}`,
	} {
		if ms := ExtractMessages([]byte(l)); len(ms) != 0 {
			t.Fatalf("expected injected text skipped: %+v", ms)
		}
	}
}
