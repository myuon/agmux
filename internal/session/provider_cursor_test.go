package session

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCursorProvider_Name(t *testing.T) {
	p := NewCursorProvider("")
	if p.Name() != ProviderCursor {
		t.Errorf("expected %q, got %q", ProviderCursor, p.Name())
	}
}

func TestCursorProvider_DefaultCommand(t *testing.T) {
	p := NewCursorProvider("")
	if p.command != "agent" {
		t.Errorf("expected default command %q, got %q", "agent", p.command)
	}
}

func TestCursorProvider_CustomCommand(t *testing.T) {
	p := NewCursorProvider("cursor-agent")
	if p.command != "cursor-agent" {
		t.Errorf("expected command %q, got %q", "cursor-agent", p.command)
	}
}

func TestCursorProvider_BuildStreamCommand_NewSession(t *testing.T) {
	p := NewCursorProvider("agent")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:   "sess-1",
		ProjectPath: "/tmp/project",
	})

	args := cmd.Args
	if args[0] != "agent" {
		t.Errorf("expected command %q, got %q", "agent", args[0])
	}
	if cmd.Dir != "/tmp/project" {
		t.Errorf("expected dir %q, got %q", "/tmp/project", cmd.Dir)
	}

	for _, expected := range []string{"-p", "--output-format", "stream-json", "--trust", "--force", "--workspace", "/tmp/project"} {
		if !containsArg(args[1:], expected) {
			t.Errorf("expected args to contain %q, got: %v", expected, args[1:])
		}
	}

	// New session (no resume) should not include --resume.
	for _, a := range args {
		if a == "--resume" {
			t.Errorf("new session should not contain --resume, got: %v", args)
		}
	}
}

func TestCursorProvider_BuildStreamCommand_Resume(t *testing.T) {
	p := NewCursorProvider("agent")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:    "sess-1",
		ProjectPath:  "/tmp/project",
		Resume:       true,
		CLISessionID: "chat-abc123",
	})

	args := cmd.Args
	if !containsArg(args, "--resume") {
		t.Errorf("expected --resume in args, got: %v", args)
	}
	if !containsArg(args, "chat-abc123") {
		t.Errorf("expected session ID 'chat-abc123' in args, got: %v", args)
	}
	if !containsArg(args, "--workspace") {
		t.Errorf("expected --workspace in args, got: %v", args)
	}
}

func TestCursorProvider_BuildStreamCommand_ResumeWithFollowup(t *testing.T) {
	p := NewCursorProvider("agent")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:     "sess-1",
		ProjectPath:   "/tmp/project",
		Resume:        true,
		CLISessionID:  "chat-abc",
		InitialPrompt: "follow up message",
	})

	args := cmd.Args
	lastArg := args[len(args)-1]
	if lastArg != "follow up message" {
		t.Errorf("expected last arg to be followup message %q, got %q", "follow up message", lastArg)
	}
}

func TestCursorProvider_BuildStreamCommand_WithModel(t *testing.T) {
	p := NewCursorProvider("agent")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:   "sess-1",
		ProjectPath: "/tmp/project",
		Model:       "sonnet-4",
	})

	args := cmd.Args
	foundModel := false
	for i, a := range args {
		if a == "--model" && i+1 < len(args) && args[i+1] == "sonnet-4" {
			foundModel = true
			break
		}
	}
	if !foundModel {
		t.Errorf("expected --model sonnet-4 in args, got: %v", args)
	}
}

func TestCursorProvider_BuildStreamCommand_WithInitialPrompt(t *testing.T) {
	p := NewCursorProvider("agent")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:     "sess-1",
		ProjectPath:   "/tmp/project",
		InitialPrompt: "hello world",
	})

	args := cmd.Args
	lastArg := args[len(args)-1]
	if lastArg != "hello world" {
		t.Errorf("expected last arg %q, got %q", "hello world", lastArg)
	}
}

func TestCursorProvider_BuildStreamCommand_WithSystemPrompt(t *testing.T) {
	p := NewCursorProvider("agent")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:     "sess-1",
		ProjectPath:   "/tmp/project",
		SystemPrompt:  "be helpful",
		InitialPrompt: "do a thing",
	})

	args := cmd.Args
	lastArg := args[len(args)-1]
	if !strings.Contains(lastArg, "be helpful") {
		t.Errorf("expected system prompt in prompt arg, got: %q", lastArg)
	}
	if !strings.Contains(lastArg, "do a thing") {
		t.Errorf("expected initial prompt in prompt arg, got: %q", lastArg)
	}
}

func TestCursorProvider_BuildStreamCommand_EnvFiltersCLAUDECODE(t *testing.T) {
	p := NewCursorProvider("agent")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:   "sess-1",
		ProjectPath: "/tmp",
	})

	for _, env := range cmd.Env {
		if strings.HasPrefix(env, "CLAUDECODE=") {
			t.Errorf("CLAUDECODE env var should be filtered out, found: %s", env)
		}
	}
}

func TestCursorProvider_ParseSessionID(t *testing.T) {
	p := NewCursorProvider("")

	tests := []struct {
		name   string
		input  string
		wantID string
		wantOK bool
	}{
		{
			name:   "system init event",
			input:  `{"type":"system","subtype":"init","session_id":"abc-123","cwd":"/tmp","model":"sonnet-4"}`,
			wantID: "abc-123",
			wantOK: true,
		},
		{
			name:   "system event without session_id",
			input:  `{"type":"system","subtype":"init"}`,
			wantID: "",
			wantOK: false,
		},
		{
			name:   "non-system event",
			input:  `{"type":"assistant","message":{}}`,
			wantID: "",
			wantOK: false,
		},
		{
			name:   "invalid JSON",
			input:  `not json`,
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

func TestCursorProvider_ParseModel(t *testing.T) {
	p := NewCursorProvider("")

	tests := []struct {
		name      string
		input     string
		wantModel string
		wantOK    bool
	}{
		{
			name:      "system init with model",
			input:     `{"type":"system","subtype":"init","session_id":"s","model":"sonnet-4"}`,
			wantModel: "sonnet-4",
			wantOK:    true,
		},
		{
			name:      "assistant with model",
			input:     `{"type":"assistant","message":{"model":"sonnet-4","role":"assistant"}}`,
			wantModel: "sonnet-4",
			wantOK:    true,
		},
		{
			name:      "system without model",
			input:     `{"type":"system","subtype":"init","session_id":"s"}`,
			wantModel: "",
			wantOK:    false,
		},
		{
			name:      "invalid JSON",
			input:     `not json`,
			wantModel: "",
			wantOK:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, ok := p.ParseModel([]byte(tt.input))
			if model != tt.wantModel || ok != tt.wantOK {
				t.Errorf("ParseModel(%q) = (%q, %v), want (%q, %v)",
					tt.input, model, ok, tt.wantModel, tt.wantOK)
			}
		})
	}
}

func TestCursorProvider_SetupMCP_Noop(t *testing.T) {
	p := NewCursorProvider("agent")
	path, err := p.SetupMCP("sess-test", 4321)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if path != "" {
		t.Errorf("expected empty path, got %q", path)
	}
}

func TestCursorProvider_CleanupMCP_Noop(t *testing.T) {
	p := NewCursorProvider("agent")
	if err := p.CleanupMCP("sess-test"); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestCursorProvider_AppendOTelEnv_Noop(t *testing.T) {
	p := NewCursorProvider("agent")
	initial := []string{"FOO=bar"}
	got := p.AppendOTelEnv(initial, 4321)
	if len(got) != len(initial) {
		t.Errorf("expected env to be unchanged, got: %v", got)
	}
}

func TestCursorProvider_NormalizeStreamLine(t *testing.T) {
	p := NewCursorProvider("")

	tests := []struct {
		name     string
		input    string
		wantNil  bool
		wantJSON string // empty means keep original
	}{
		{
			name:  "system init passes through",
			input: `{"type":"system","subtype":"init","session_id":"abc","model":"sonnet-4"}`,
		},
		{
			name:  "assistant passes through",
			input: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`,
		},
		{
			name:  "user passes through",
			input: `{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`,
		},
		{
			name:  "result passes through",
			input: `{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"abc"}`,
		},
		{
			name:    "tool_call started is dropped",
			input:   `{"type":"tool_call","subtype":"started","name":"bash","input":{"cmd":"ls"}}`,
			wantNil: true,
		},
		{
			name:     "tool_call completed becomes assistant tool_use+tool_result",
			input:    `{"type":"tool_call","subtype":"completed","name":"bash","input":{"command":"ls"},"result":"file1\nfile2"}`,
			wantJSON: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"bash","input":{"command":"ls"}},{"type":"tool_result","content":"file1\nfile2"}]}}`,
		},
		{
			name:  "invalid JSON passes through",
			input: `not json at all`,
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
				if string(result) != tt.input {
					t.Errorf("expected line to pass through unchanged\ngot:  %s\nwant: %s", string(result), tt.input)
				}
			}
		})
	}
}
