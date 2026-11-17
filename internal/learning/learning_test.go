package learning

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

type env struct {
	r       *Reviewer
	streams string
	home    string
	project string
	prompts []string
	started []string
}

func setup(t *testing.T, output string) *env {
	t.Helper()
	dir := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	e := &env{streams: filepath.Join(dir, "streams"), home: filepath.Join(dir, "home"), project: filepath.Join(dir, "proj")}
	for _, d := range []string{e.streams, e.home, e.project} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(e.project, "CLAUDE.md"), []byte("# 既存ルール\n- make build を使う\n"), 0o644)
	store, err := NewStore(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	e.r = NewReviewer(store, sqlDB, e.streams, e.home, "claude", slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.r.Generate = func(ctx context.Context, model, prompt string) (string, error) {
		e.prompts = append(e.prompts, prompt)
		return output, nil
	}
	e.r.StartSession = func(name, projectPath, prompt string) (string, error) {
		e.started = append(e.started, prompt)
		return "apply1", nil
	}
	if _, err := sqlDB.Exec(`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider) VALUES ('s1', '作業', ?, '', 'idle', 'worker', 'stream', 'claude')`, e.project); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) appendTranscript(t *testing.T, text string) {
	f, _ := os.OpenFile(filepath.Join(e.streams, "s1.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	defer f.Close()
	f.WriteString(`{"type":"user","message":{"content":"` + text + `"}}` + "\n")
}

const sampleOutput = "```json\n" + `{"proposals":[
 {"target":"memory","name":"Keychain Fix","memory_type":"feedback","title":"Keychain の破損","content":"expiresAt=0 は破損。\n**Why:** x\n**How to apply:** y","reason":"ユーザーが指摘"},
 {"target":"claude_md","title":"テストは make test","content":"- テストは make test で回す","reason":"失敗から"},
 {"target":"bogus","title":"x","content":"y"},
 {"target":"skill","name":"","title":"no name","content":"z"}
]}` + "\n```"

func TestReviewCreatesProposalsAndIsIncremental(t *testing.T) {
	e := setup(t, sampleOutput)
	e.appendTranscript(t, strings.Repeat("長い作業の会話。", 100))

	n, err := e.r.Review(context.Background(), "s1")
	if err != nil || n != 2 {
		t.Fatalf("expected 2 valid proposals, got n=%d err=%v", n, err)
	}
	if !strings.Contains(e.prompts[0], "make build を使う") || !strings.Contains(e.prompts[0], "長い作業の会話") {
		t.Fatal("prompt should include CLAUDE.md and transcript")
	}
	ps, _ := e.r.store.List(StatusPending, 10)
	if len(ps) != 2 || ps[1].MemoryType != "feedback" || ps[1].Name != "keychain-fix" {
		t.Fatalf("unexpected proposals: %+v %+v", ps[0], ps[1])
	}

	// No new conversation → nothing reviewed again.
	if n, _ := e.r.Review(context.Background(), "s1"); n != 0 || len(e.prompts) != 1 {
		t.Fatalf("expected no re-review, n=%d prompts=%d", n, len(e.prompts))
	}

	// Short additions are skipped too.
	e.appendTranscript(t, "ok")
	if n, _ := e.r.Review(context.Background(), "s1"); n != 0 || len(e.prompts) != 1 {
		t.Fatal("short transcript should not be reviewed")
	}
}

func TestAcceptMemoryWritesFileAndIndex(t *testing.T) {
	e := setup(t, sampleOutput)
	e.appendTranscript(t, strings.Repeat("会話", 300))
	e.r.Review(context.Background(), "s1")
	ps, _ := e.r.store.List(StatusPending, 10)
	var mem *Proposal
	for _, p := range ps {
		if p.Target == TargetMemory {
			mem = p
		}
	}
	got, err := e.r.Accept(mem.ID, "編集後の本文")
	if err != nil || got.Status != StatusApplied {
		t.Fatalf("accept failed: %v %+v", err, got)
	}
	dir := MemoryDir(e.home, e.project)
	body, err := os.ReadFile(filepath.Join(dir, "keychain-fix.md"))
	if err != nil || !strings.Contains(string(body), "編集後の本文") || !strings.Contains(string(body), "type: feedback") {
		t.Fatalf("memory file wrong: %v\n%s", err, body)
	}
	idx, _ := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if !strings.Contains(string(idx), "(keychain-fix.md)") {
		t.Fatalf("index not updated: %s", idx)
	}
	if _, err := e.r.Accept(mem.ID, ""); err == nil {
		t.Fatal("accepting twice must fail")
	}
}

func TestAcceptRepoTargetStartsSessionAndRejectFeedsBack(t *testing.T) {
	e := setup(t, sampleOutput)
	e.appendTranscript(t, strings.Repeat("会話", 300))
	e.r.Review(context.Background(), "s1")
	ps, _ := e.r.store.List(StatusPending, 10)
	var cm, mem *Proposal
	for _, p := range ps {
		switch p.Target {
		case TargetClaudeMD:
			cm = p
		case TargetMemory:
			mem = p
		}
	}
	got, err := e.r.Accept(cm.ID, "")
	if err != nil || got.Status != StatusApplying || got.ApplySessionID != "apply1" {
		t.Fatalf("expected applying with session: %v %+v", err, got)
	}
	if len(e.started) != 1 || !strings.Contains(e.started[0], "gh pr create") || !strings.Contains(e.started[0], "テストは make test で回す") {
		t.Fatalf("apply prompt wrong: %v", e.started)
	}
	// CLAUDE.md itself is untouched by agmux.
	if b, _ := os.ReadFile(filepath.Join(e.project, "CLAUDE.md")); strings.Contains(string(b), "make test") {
		t.Fatal("CLAUDE.md must not be written directly")
	}

	if _, err := e.r.Reject(mem.ID); err != nil {
		t.Fatal(err)
	}
	e.appendTranscript(t, strings.Repeat("続きの会話", 200))
	e.r.Review(context.Background(), "s1")
	if !strings.Contains(e.prompts[1], "[却下済み] (memory) Keychain の破損") {
		t.Fatalf("rejected proposal should be in the next prompt:\n%s", e.prompts[1])
	}
}

func TestParseProposalsTolerant(t *testing.T) {
	ps, err := ParseProposals("はい。\n{\"proposals\":[]}\n以上")
	if err != nil || len(ps) != 0 {
		t.Fatalf("got %v %v", ps, err)
	}
	if _, err := ParseProposals("not json"); err == nil {
		t.Fatal("expected error")
	}
}
