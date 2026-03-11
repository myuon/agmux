package session

import (
	"encoding/json"
	"strings"
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

func TestCodexProvider_BuildStreamCommand_MCPIgnored(t *testing.T) {
	p := NewCodexProvider("codex")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:     "sess-1",
		ProjectPath:   "/tmp/project",
		MCPConfigPath: "/tmp/mcp.json",
	})

	args := cmd.Args
	for _, a := range args {
		if a == "--mcp-config" {
			t.Errorf("--mcp-config should not be in args for Codex, got: %v", args)
		}
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
	for _, part := range []string{"codex", "--sandbox", "danger-full-access"} {
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

func TestCodexProvider_BuildStreamCommand_WithInitialPrompt(t *testing.T) {
	p := NewCodexProvider("codex")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:      "sess-1",
		ProjectPath:    "/tmp/project",
		InitialPrompt:  "hello world",
	})

	args := cmd.Args
	// The last arg should be the initial prompt, not the placeholder
	lastArg := args[len(args)-1]
	if lastArg != "hello world" {
		t.Errorf("expected last arg to be initial prompt %q, got %q", "hello world", lastArg)
	}
}

func TestCodexProvider_BuildStreamCommand_ResumeWithFollowup(t *testing.T) {
	p := NewCodexProvider("codex")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:      "sess-1",
		ProjectPath:    "/tmp/project",
		Resume:         true,
		CLISessionID:   "thr_abc123",
		InitialPrompt:  "follow up message",
	})

	args := cmd.Args
	// Should contain resume and session ID
	if !containsArg(args, "resume") {
		t.Errorf("expected args to contain 'resume', got: %v", args)
	}
	if !containsArg(args, "thr_abc123") {
		t.Errorf("expected args to contain session ID, got: %v", args)
	}
	// Last arg should be the followup message
	lastArg := args[len(args)-1]
	if lastArg != "follow up message" {
		t.Errorf("expected last arg to be followup message %q, got %q", "follow up message", lastArg)
	}
}

func TestCodexProvider_BuildStreamCommand_DefaultPlaceholder(t *testing.T) {
	p := NewCodexProvider("codex")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:   "sess-1",
		ProjectPath: "/tmp/project",
	})

	args := cmd.Args
	lastArg := args[len(args)-1]
	if lastArg != "Follow the instructions given via stdin" {
		t.Errorf("expected default placeholder prompt, got %q", lastArg)
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

func TestCodexBuildTerminalCommand_New(t *testing.T) {
	p := NewCodexProvider("")
	cmd := p.BuildTerminalCommand(TerminalOpts{
		SessionID:     "sess-123",
		MCPConfigPath: "/tmp/mcp.json",
	})

	if !strings.Contains(cmd, "codex") {
		t.Errorf("expected codex command, got %s", cmd)
	}
	if !strings.Contains(cmd, "--sandbox danger-full-access") {
		t.Errorf("expected --sandbox flag, got %s", cmd)
	}
	if strings.Contains(cmd, "--mcp-config") {
		t.Errorf("--mcp-config should not be in Codex command, got %s", cmd)
	}
	if strings.Contains(cmd, "resume") {
		t.Errorf("new session should not contain resume, got %s", cmd)
	}
}

func TestCodexBuildTerminalCommand_Resume(t *testing.T) {
	p := NewCodexProvider("")
	cmd := p.BuildTerminalCommand(TerminalOpts{
		SessionID:     "sess-456",
		MCPConfigPath: "/tmp/mcp.json",
		Resume:        true,
	})

	if !strings.Contains(cmd, "codex resume sess-456") {
		t.Errorf("expected resume with session ID, got %s", cmd)
	}
	if !strings.Contains(cmd, "--sandbox danger-full-access") {
		t.Errorf("expected --sandbox flag, got %s", cmd)
	}
}

func TestCodexBuildTerminalCommand_WithSystemPrompt(t *testing.T) {
	p := NewCodexProvider("")
	cmd := p.BuildTerminalCommand(TerminalOpts{
		SessionID:     "sess-789",
		MCPConfigPath: "/tmp/mcp.json",
		SystemPrompt:  "You are a helpful assistant",
	})

	if !strings.Contains(cmd, "--instructions") {
		t.Errorf("expected --instructions flag, got %s", cmd)
	}
	if !strings.Contains(cmd, "You are a helpful assistant") {
		t.Errorf("expected system prompt in command, got %s", cmd)
	}
}

func TestCodexBuildTerminalCommand_CustomCommand(t *testing.T) {
	p := NewCodexProvider("my-codex")
	cmd := p.BuildTerminalCommand(TerminalOpts{
		SessionID:     "sess-100",
		MCPConfigPath: "/tmp/mcp.json",
	})

	if !strings.HasPrefix(cmd, "my-codex") {
		t.Errorf("expected custom command, got %s", cmd)
	}
}

func TestCodexBuildTerminalCommand_NoMCPConfig(t *testing.T) {
	p := NewCodexProvider("")
	cmd := p.BuildTerminalCommand(TerminalOpts{
		SessionID: "sess-200",
	})

	if strings.Contains(cmd, "--mcp-config") {
		t.Errorf("should not have --mcp-config when path is empty, got %s", cmd)
	}
}

func TestCodexProvider_NormalizeStreamLine(t *testing.T) {
	p := NewCodexProvider("")

	tests := []struct {
		name     string
		input    string
		wantNil  bool   // true if the line should be dropped
		wantJSON string // expected JSON (empty means keep original)
	}{
		{
			name:  "agent_message becomes assistant text",
			input: `{"type":"item.completed","item":{"type":"agent_message","text":"Hello world"}}`,
			wantJSON: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hello world"}]}}`,
		},
		{
			name:  "command_execution becomes tool_use + tool_result",
			input: `{"type":"item.completed","item":{"type":"command_execution","command":"ls","aggregated_output":"file1\nfile2","exit_code":0,"status":"completed"}}`,
			wantJSON: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"ls"}},{"type":"tool_result","content":"file1\nfile2"}]}}`,
		},
		{
			name:    "item.started is dropped",
			input:   `{"type":"item.started","item":{"type":"command_execution","command":"ls","aggregated_output":"","exit_code":null,"status":"in_progress"}}`,
			wantNil: true,
		},
		{
			name:  "turn.started passes through",
			input: `{"type":"turn.started"}`,
		},
		{
			name:  "turn.completed passes through",
			input: `{"type":"turn.completed","usage":{"input_tokens":123,"output_tokens":456}}`,
		},
		{
			name:  "thread.started passes through",
			input: `{"type":"thread.started","thread":{"id":"thr_xxx"}}`,
		},
		{
			name:  "invalid JSON passes through",
			input: `not json at all`,
		},
		{
			name:  "unknown item type passes through",
			input: `{"type":"item.completed","item":{"type":"unknown_type","data":"something"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := p.NormalizeStreamLine([]byte(tt.input))

			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %s", string(result))
				}
				return
			}

			if result == nil {
				t.Fatal("expected non-nil result")
			}

			if tt.wantJSON != "" {
				// Compare as parsed JSON for stable comparison
				var got, want interface{}
				if err := json.Unmarshal(result, &got); err != nil {
					t.Fatalf("failed to parse result JSON: %v\nresult: %s", err, string(result))
				}
				if err := json.Unmarshal([]byte(tt.wantJSON), &want); err != nil {
					t.Fatalf("failed to parse expected JSON: %v", err)
				}
				gotBytes, _ := json.Marshal(got)
				wantBytes, _ := json.Marshal(want)
				if string(gotBytes) != string(wantBytes) {
					t.Errorf("JSON mismatch\ngot:  %s\nwant: %s", string(gotBytes), string(wantBytes))
				}
			} else {
				// Should be unchanged
				if string(result) != tt.input {
					t.Errorf("expected line to pass through unchanged\ngot:  %s\nwant: %s", string(result), tt.input)
				}
			}
		})
	}
}

func TestClaudeProvider_NormalizeStreamLine(t *testing.T) {
	p := NewClaudeProvider("")
	input := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}`
	result := p.NormalizeStreamLine([]byte(input))
	if string(result) != input {
		t.Errorf("ClaudeProvider should pass through unchanged\ngot:  %s\nwant: %s", string(result), input)
	}
}
