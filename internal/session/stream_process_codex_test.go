package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	// Expected: codex exec resume --json thr_abc123 "follow up message"
	expected := []string{"codex", "exec", "resume", "--json", "thr_abc123", "follow up message"}
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
	// Expected: codex exec resume --json thr_abc123
	expected := []string{"codex", "exec", "resume", "--json", "thr_abc123"}
	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i, want := range expected {
		if args[i] != want {
			t.Errorf("args[%d] = %q, want %q (full args: %v)", i, args[i], want, args)
		}
	}
}

func TestStreamProcess_SessionIDInitializedFromOpts(t *testing.T) {
	// Verify that when CLISessionID is provided in opts, sp.sessionID is initialized.
	// This is critical for recovery/resume: sp.sessionID must be set so sendCodex can use it.
	streamsDir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "test-init-sid-" + t.Name()
	streamPath := filepath.Join(streamsDir, sessionID+".jsonl")
	// Ensure cleanup
	defer os.Remove(streamPath)

	// Use a stub provider that runs "true" (exits immediately)
	provider := &stubCodexProvider{CodexProvider: *NewCodexProvider("")}

	sp, err := StartStreamProcess(sessionID, t.TempDir(), "", "", true, false, provider, "pre-existing-thread-id")
	if err != nil {
		t.Fatalf("StartStreamProcess failed: %v", err)
	}
	defer sp.Stop()

	// The sessionID should be initialized from opts
	got := sp.SessionID()
	if got != "pre-existing-thread-id" {
		t.Errorf("SessionID() = %q, want %q", got, "pre-existing-thread-id")
	}
}

func TestStreamProcess_SessionIDUpdatedByReadLoop(t *testing.T) {
	// Verify that readLoop updates sp.sessionID when it sees thread.started
	streamsDir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "test-readloop-sid-" + t.Name()
	streamPath := filepath.Join(streamsDir, sessionID+".jsonl")
	defer os.Remove(streamPath)

	threadID := "019cdd95-new-thread-from-readloop"
	// Use a stub that outputs a thread.started event
	provider := &stubCodexProviderWithOutput{
		CodexProvider: *NewCodexProvider(""),
		output:        `{"type":"thread.started","thread_id":"` + threadID + `"}`,
	}

	sp, err := StartStreamProcess(sessionID, t.TempDir(), "", "", false, false, provider)
	if err != nil {
		t.Fatalf("StartStreamProcess failed: %v", err)
	}
	defer sp.Stop()

	// Wait for process to finish
	<-sp.Done()

	got := sp.SessionID()
	if got != threadID {
		t.Errorf("SessionID() = %q, want %q", got, threadID)
	}

	// Also verify the thread.started event was written to the JSONL file
	fileContent, err := os.ReadFile(streamPath)
	if err != nil {
		t.Fatalf("failed to read stream file: %v", err)
	}
	if len(fileContent) == 0 {
		t.Fatal("stream file is empty, expected thread.started event")
	}
}

// TestSendCodex_FollowupAfterProcessExit verifies that sendCodex correctly
// restarts the process when the initial Codex process has already exited.
// This is the core bug fix for issue #199.
func TestSendCodex_FollowupAfterProcessExit(t *testing.T) {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "test-codex-followup-" + t.Name()
	streamPath := filepath.Join(streamsDir, sessionID+".jsonl")
	defer os.Remove(streamPath)

	threadID := "019cdd95-followup-test-thread"
	// Provider that outputs thread.started then exits (simulates initial Codex run)
	provider := &stubCodexProviderWithOutput{
		CodexProvider: *NewCodexProvider(""),
		output:        `{"type":"thread.started","thread_id":"` + threadID + `"}`,
	}

	// Start the initial process (simulates session creation)
	sp, err := StartStreamProcess(sessionID, t.TempDir(), "", "", false, false, provider)
	if err != nil {
		t.Fatalf("StartStreamProcess failed: %v", err)
	}
	defer sp.Stop()

	// Wait for initial process to finish (simulates Codex exec completing)
	<-sp.Done()

	// Verify session ID was captured
	if got := sp.SessionID(); got != threadID {
		t.Fatalf("SessionID() = %q, want %q", got, threadID)
	}

	// Now send a followup message (this is the bug scenario from #199)
	// Switch to a provider that also outputs something for the followup
	provider.output = `{"type":"thread.started","thread_id":"` + threadID + `"}
{"type":"item.completed","item":{"type":"agent_message","text":"followup response"}}`

	err = sp.sendCodex("followup message")
	if err != nil {
		t.Fatalf("sendCodex() returned error: %v", err)
	}

	// Wait for the restarted process to finish
	<-sp.Done()

	// Verify stream file contains the followup user message and response
	content, err := os.ReadFile(streamPath)
	if err != nil {
		t.Fatalf("failed to read stream file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, `"followup message"`) {
		t.Errorf("stream file should contain followup user message, got:\n%s", contentStr)
	}
	if !strings.Contains(contentStr, `followup response`) {
		t.Errorf("stream file should contain followup response, got:\n%s", contentStr)
	}
}

// TestSendCodex_MultipleFollowups verifies that multiple followup messages work.
func TestSendCodex_MultipleFollowups(t *testing.T) {
	streamsDir, err := db.StreamsDir()
	if err != nil {
		t.Fatal(err)
	}

	sessionID := "test-codex-multi-followup-" + t.Name()
	streamPath := filepath.Join(streamsDir, sessionID+".jsonl")
	defer os.Remove(streamPath)

	threadID := "019cdd95-multi-followup-thread"
	provider := &stubCodexProviderWithOutput{
		CodexProvider: *NewCodexProvider(""),
		output:        `{"type":"thread.started","thread_id":"` + threadID + `"}`,
	}

	sp, err := StartStreamProcess(sessionID, t.TempDir(), "", "", false, false, provider)
	if err != nil {
		t.Fatalf("StartStreamProcess failed: %v", err)
	}
	defer sp.Stop()

	<-sp.Done()

	// Send first followup
	provider.output = `{"type":"thread.started","thread_id":"` + threadID + `"}`
	if err := sp.sendCodex("first followup"); err != nil {
		t.Fatalf("first sendCodex() returned error: %v", err)
	}
	<-sp.Done()

	// Send second followup
	provider.output = `{"type":"thread.started","thread_id":"` + threadID + `"}`
	if err := sp.sendCodex("second followup"); err != nil {
		t.Fatalf("second sendCodex() returned error: %v", err)
	}
	<-sp.Done()

	// Verify both messages are in the stream file
	content, err := os.ReadFile(streamPath)
	if err != nil {
		t.Fatalf("failed to read stream file: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, `"first followup"`) {
		t.Errorf("stream file should contain first followup message")
	}
	if !strings.Contains(contentStr, `"second followup"`) {
		t.Errorf("stream file should contain second followup message")
	}
}

// stubCodexProvider wraps CodexProvider but overrides BuildStreamCommand to use "true"
// (exits immediately with success).
type stubCodexProvider struct {
	CodexProvider
}

func (p *stubCodexProvider) BuildStreamCommand(opts StreamOpts) *exec.Cmd {
	cmd := exec.Command("true")
	cmd.Dir = opts.ProjectPath
	return cmd
}

// stubCodexProviderWithOutput overrides BuildStreamCommand to echo specified output.
type stubCodexProviderWithOutput struct {
	CodexProvider
	output string
}

func (p *stubCodexProviderWithOutput) BuildStreamCommand(opts StreamOpts) *exec.Cmd {
	cmd := exec.Command("echo", p.output)
	cmd.Dir = opts.ProjectPath
	return cmd
}
