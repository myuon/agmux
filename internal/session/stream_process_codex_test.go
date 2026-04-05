package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/myuon/agmux/internal/db"
)

func TestReadCLISessionID_Codex(t *testing.T) {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}

	provider := NewCodexProvider("")
	sessionID := "test-codex-read-cli-" + t.Name()

	// Write a JSONL file that simulates Codex output (normalized format)
	streamPath := filepath.Join(streamsDir, sessionID+".jsonl")
	content := `{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"thread.started","thread_id":"019cdd95-78da-7f33-8521-ae4ca1eb40d7"}
{"type":"turn.started"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hi there!"}]}}
`
	if err := os.WriteFile(streamPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(streamPath)

	// ReadCLISessionID should extract the thread_id
	got := ReadCLISessionID(sessionID, provider)
	want := "019cdd95-78da-7f33-8521-ae4ca1eb40d7"
	if got != want {
		t.Errorf("ReadCLISessionID() = %q, want %q", got, want)
	}
}

func TestReadCLISessionID_Codex_NoThreadStarted(t *testing.T) {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}

	provider := NewCodexProvider("")
	sessionID := "test-codex-no-thread-" + t.Name()

	streamPath := filepath.Join(streamsDir, sessionID+".jsonl")
	content := `{"type":"user","message":{"role":"user","content":"hello"}}
{"type":"turn.started"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hi"}]}}
`
	if err := os.WriteFile(streamPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(streamPath)

	got := ReadCLISessionID(sessionID, provider)
	if got != "" {
		t.Errorf("ReadCLISessionID() = %q, want empty string", got)
	}
}

func TestReadCLISessionID_Codex_MultipleThreads(t *testing.T) {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}

	provider := NewCodexProvider("")
	sessionID := "test-codex-multi-thread-" + t.Name()

	streamPath := filepath.Join(streamsDir, sessionID+".jsonl")
	content := `{"type":"thread.started","thread_id":"first-thread-id"}
{"type":"turn.started"}
{"type":"thread.started","thread_id":"second-thread-id"}
{"type":"turn.started"}
`
	if err := os.WriteFile(streamPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(streamPath)

	// Should return the LAST thread_id
	got := ReadCLISessionID(sessionID, provider)
	want := "second-thread-id"
	if got != want {
		t.Errorf("ReadCLISessionID() = %q, want %q", got, want)
	}
}

func TestReadCLISessionID_Codex_FileNotFound(t *testing.T) {
	provider := NewCodexProvider("")
	got := ReadCLISessionID("nonexistent-session-id-xyz", provider)
	if got != "" {
		t.Errorf("ReadCLISessionID() = %q, want empty string", got)
	}
}

func TestReadCLISessionID_Codex_WithHelperJSON(t *testing.T) {
	// Verify that NewCodexThreadStartedJSON output is parseable by ReadCLISessionID
	streamsDir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}

	provider := NewCodexProvider("")
	sessionID := "test-codex-helper-" + t.Name()

	threadLine := NewCodexThreadStartedJSON("thr_helper_test")
	streamPath := filepath.Join(streamsDir, sessionID+".jsonl")
	if err := os.WriteFile(streamPath, []byte(threadLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(streamPath)

	got := ReadCLISessionID(sessionID, provider)
	want := "thr_helper_test"
	if got != want {
		t.Errorf("ReadCLISessionID() = %q, want %q", got, want)
	}
}

func TestCodexProvider_BuildStreamCommand_ResumeExactArgs(t *testing.T) {
	p := NewCodexProvider("codex")

	// Verify the exact argument order for resume with followup
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:     "sess-1",
		ProjectPath:   "/tmp/project",
		Resume:        true,
		CLISessionID:  "thr_abc123",
		InitialPrompt: "follow up message",
	})

	args := cmd.Args
	// Expected: codex exec resume --json --dangerously-bypass-approvals-and-sandbox thr_abc123 "follow up message"
	expected := []string{"codex", "exec", "resume", "--json", "--dangerously-bypass-approvals-and-sandbox", "thr_abc123", "follow up message"}
	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i, want := range expected {
		if args[i] != want {
			t.Errorf("args[%d] = %q, want %q (full args: %v)", i, args[i], want, args)
		}
	}
}

func TestCodexProvider_BuildStreamCommand_ResumeNoPromptExactArgs(t *testing.T) {
	p := NewCodexProvider("codex")

	// Resume without followup prompt
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:    "sess-1",
		ProjectPath:  "/tmp/project",
		Resume:       true,
		CLISessionID: "thr_abc123",
	})

	args := cmd.Args
	// Expected: codex exec resume --json --dangerously-bypass-approvals-and-sandbox thr_abc123
	expected := []string{"codex", "exec", "resume", "--json", "--dangerously-bypass-approvals-and-sandbox", "thr_abc123"}
	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i, want := range expected {
		if args[i] != want {
			t.Errorf("args[%d] = %q, want %q (full args: %v)", i, args[i], want, args)
		}
	}
}
