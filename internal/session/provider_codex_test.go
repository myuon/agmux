package session

import (
	"encoding/json"
	"testing"
)

func TestCodexProvider_Name(t *testing.T) {
	p := NewCodexProvider("")
	if p.Name() != ProviderCodex {
		t.Errorf("expected %q, got %q", ProviderCodex, p.Name())
	}
}

func TestCodexProvider_DefaultCommand(t *testing.T) {
	p := NewCodexProvider("")
	if p.command != "codex" {
		t.Errorf("expected default command %q, got %q", "codex", p.command)
	}
}

func TestCodexProvider_CustomCommand(t *testing.T) {
	p := NewCodexProvider("my-codex")
	if p.command != "my-codex" {
		t.Errorf("expected command %q, got %q", "my-codex", p.command)
	}
}

func TestCodexProvider_BuildStreamCommand_NewSession(t *testing.T) {
	p := NewCodexProvider("codex")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:   "sess-1",
		ProjectPath: "/tmp/project",
		SystemPrompt: "test prompt",
	})

	args := cmd.Args
	if args[0] != "codex" {
		t.Errorf("expected command %q, got %q", "codex", args[0])
	}
	if cmd.Dir != "/tmp/project" {
		t.Errorf("expected dir %q, got %q", "/tmp/project", cmd.Dir)
	}

	// Check required args are present
	argsStr := joinArgs(args[1:])
	for _, expected := range []string{"exec", "--json", "--sandbox", "danger-full-access"} {
		if !containsArg(args[1:], expected) {
			t.Errorf("expected args to contain %q, got: %s", expected, argsStr)
		}
	}
}

func TestCodexProvider_BuildStreamCommand_Resume(t *testing.T) {
	p := NewCodexProvider("codex")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:    "sess-1",
		ProjectPath:  "/tmp/project",
		Resume:       true,
		CLISessionID: "thr_abc123",
	})

	args := cmd.Args
	// Should contain "resume" and the session ID
	if !containsArg(args, "resume") {
		t.Errorf("expected args to contain 'resume', got: %v", args)
	}
	if !containsArg(args, "thr_abc123") {
		t.Errorf("expected args to contain session ID 'thr_abc123', got: %v", args)
	}
}

func TestCodexProvider_BuildStreamCommand_WithMCP(t *testing.T) {
	p := NewCodexProvider("codex")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:     "sess-1",
		ProjectPath:   "/tmp/project",
		MCPConfigPath: "/tmp/mcp.json",
	})

	args := cmd.Args
	found := false
	for i, a := range args {
		if a == "--mcp-config" && i+1 < len(args) && args[i+1] == "/tmp/mcp.json" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --mcp-config /tmp/mcp.json in args, got: %v", args)
	}
}

func TestCodexProvider_BuildStreamCommand_WithInstructions(t *testing.T) {
	p := NewCodexProvider("codex")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:    "sess-1",
		ProjectPath:  "/tmp/project",
		SystemPrompt: "be helpful",
	})

	args := cmd.Args
	found := false
	for i, a := range args {
		if a == "--instructions" && i+1 < len(args) && args[i+1] == "be helpful" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --instructions 'be helpful' in args, got: %v", args)
	}
}

func TestCodexProvider_ParseSessionID(t *testing.T) {
	p := NewCodexProvider("")

	tests := []struct {
		name     string
		input    string
		wantID   string
		wantOK   bool
	}{
		{
			name:   "thread.started event",
			input:  `{"type":"thread.started","thread":{"id":"thr_abc123"}}`,
			wantID: "thr_abc123",
			wantOK: true,
		},
		{
			name:   "other event type",
			input:  `{"type":"turn.started","turn":{"id":"turn_xyz"}}`,
			wantID: "",
			wantOK: false,
		},
		{
			name:   "invalid JSON",
			input:  `not json`,
			wantID: "",
			wantOK: false,
		},
		{
			name:   "thread.started without id",
			input:  `{"type":"thread.started","thread":{}}`,
			wantID: "",
			wantOK: false,
		},
		{
			name:   "claude system event should not match",
			input:  `{"type":"system","session_id":"sess-abc"}`,
			wantID: "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, ok := p.ParseSessionID([]byte(tt.input))
			if id != tt.wantID || ok != tt.wantOK {
				t.Errorf("ParseSessionID(%q) = (%q, %v), want (%q, %v)",
					tt.input, id, ok, tt.wantID, tt.wantOK)
			}
		})
	}
}

func TestCodexProvider_BuildTerminalCommand(t *testing.T) {
	p := NewCodexProvider("codex")
	cmd := p.BuildTerminalCommand(TerminalOpts{
		SessionID:     "sess-1",
		MCPConfigPath: "/tmp/mcp.json",
		SystemPrompt:  "test",
	})

	if cmd == "" {
		t.Error("expected non-empty command")
	}
	// Should contain key parts
	for _, part := range []string{"codex", "exec", "--json", "--sandbox", "danger-full-access"} {
		if !containsStr(cmd, part) {
			t.Errorf("expected command to contain %q, got: %s", part, cmd)
		}
	}
}

func TestNewCodexThreadStartedJSON(t *testing.T) {
	line := NewCodexThreadStartedJSON("thr_test123")
	var evt struct {
		Type   string `json:"type"`
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if evt.Type != "thread.started" {
		t.Errorf("expected type 'thread.started', got %q", evt.Type)
	}
	if evt.Thread.ID != "thr_test123" {
		t.Errorf("expected thread.id 'thr_test123', got %q", evt.Thread.ID)
	}
}

func TestCodexProvider_EnvFiltersCLAUDECODE(t *testing.T) {
	p := NewCodexProvider("codex")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:   "sess-1",
		ProjectPath: "/tmp",
	})

	for _, env := range cmd.Env {
		if len(env) >= 10 && env[:10] == "CLAUDECODE" {
			t.Errorf("CLAUDECODE env var should be filtered out, found: %s", env)
		}
	}
}

// helpers

func containsArg(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func joinArgs(args []string) string {
	result := ""
	for i, a := range args {
		if i > 0 {
			result += " "
		}
		result += a
	}
	return result
}
