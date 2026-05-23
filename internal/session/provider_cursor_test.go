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

func TestCursorProvider_IsOneShot(t *testing.T) {
	p := NewCursorProvider("")
	if !p.IsOneShot() {
		t.Errorf("expected CursorProvider.IsOneShot() = true, got false")
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

func TestCursorProvider_BuildStreamCommand_EmptyPromptOmitsPositional(t *testing.T) {
	p := NewCursorProvider("agent")
	cmd := p.BuildStreamCommand(StreamOpts{
		SessionID:   "sess-1",
		ProjectPath: "/tmp/project",
	})

	args := cmd.Args
	// No positional prompt should be appended when InitialPrompt is empty
	// (Cursor has no stdin path, so a placeholder would only confuse users).
	for _, a := range args {
		if strings.Contains(a, "Follow the instructions given via stdin") {
			t.Errorf("did not expect stdin placeholder in args, got: %v", args)
		}
	}
	// Last arg should be the --workspace value (or some flag-style arg),
	// not a positional prompt placeholder.
	lastArg := args[len(args)-1]
	if !strings.HasPrefix(lastArg, "/") && !strings.HasPrefix(lastArg, "-") {
		t.Errorf("expected last arg to be a flag-style or path value when prompt is empty, got: %q", lastArg)
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

func TestCursorProvider_cursorToolKindToName(t *testing.T) {
	tests := map[string]string{
		"readToolCall":    "Read",
		"shellToolCall":   "Bash",
		"editToolCall":    "Edit",
		"globToolCall":    "Glob",
		"grepToolCall":    "Grep",
		"awaitToolCall":   "Await",
		"listDirToolCall": "ListDir",
		"todoToolCall":    "Todo",
		"unknownToolCall": "Unknown",
		"":                "tool",
	}
	for in, want := range tests {
		if got := cursorToolKindToName(in); got != want {
			t.Errorf("cursorToolKindToName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCursorProvider_NormalizeToolCall_MoreKinds(t *testing.T) {
	p := NewCursorProvider("")

	type extracted struct {
		Name      string
		ID        string
		ToolUseID string
		HasInput  bool
		HasResult bool
	}

	parse := func(t *testing.T, raw []byte) extracted {
		t.Helper()
		var env struct {
			Message struct {
				Content []map[string]interface{} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("failed to parse: %v\n%s", err, string(raw))
		}
		if len(env.Message.Content) != 2 {
			t.Fatalf("expected 2 content blocks, got %d\n%s", len(env.Message.Content), string(raw))
		}
		e := extracted{}
		for _, b := range env.Message.Content {
			switch b["type"] {
			case "tool_use":
				if v, ok := b["name"].(string); ok {
					e.Name = v
				}
				if v, ok := b["id"].(string); ok {
					e.ID = v
				}
				if _, ok := b["input"]; ok {
					e.HasInput = true
				}
			case "tool_result":
				if v, ok := b["tool_use_id"].(string); ok {
					e.ToolUseID = v
				}
				if v, ok := b["content"].(string); ok && v != "" {
					e.HasResult = true
				}
			}
		}
		return e
	}

	cases := []struct {
		name     string
		input    string
		wantName string
	}{
		{
			name:     "edit",
			input:    `{"type":"tool_call","subtype":"completed","call_id":"tool_e1","tool_call":{"editToolCall":{"args":{"path":"a.go","streamContent":"x"},"result":{"success":{"path":"a.go","linesAdded":1,"linesRemoved":0,"diffString":"--- a/a.go"}}}}}`,
			wantName: "Edit",
		},
		{
			name:     "glob",
			input:    `{"type":"tool_call","subtype":"completed","call_id":"tool_g1","tool_call":{"globToolCall":{"args":{"globPattern":"**/*.go"},"result":{"success":{"files":["a.go","b.go"],"totalFiles":2}}}}}`,
			wantName: "Glob",
		},
		{
			name:     "grep",
			input:    `{"type":"tool_call","subtype":"completed","call_id":"tool_p1","tool_call":{"grepToolCall":{"args":{"pattern":"foo"},"result":{"success":{"pattern":"foo","workspaceResults":[]}}}}}`,
			wantName: "Grep",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := p.NormalizeStreamLine([]byte(tc.input))
			if out == nil {
				t.Fatalf("expected normalized output, got nil")
			}
			got := parse(t, out)
			if got.Name != tc.wantName {
				t.Errorf("name = %q, want %q", got.Name, tc.wantName)
			}
			if got.ID == "" || got.ToolUseID == "" || got.ID != got.ToolUseID {
				t.Errorf("expected matching id/tool_use_id, got id=%q tool_use_id=%q", got.ID, got.ToolUseID)
			}
			if !got.HasInput {
				t.Errorf("expected input present")
			}
			if !got.HasResult {
				t.Errorf("expected result content present")
			}
		})
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
			input:   `{"type":"tool_call","subtype":"started","call_id":"tool_x","tool_call":{"readToolCall":{"args":{"path":"/a"}}}}`,
			wantNil: true,
		},
		{
			name:     "tool_call completed (read) becomes assistant tool_use+tool_result",
			input:    `{"type":"tool_call","subtype":"completed","call_id":"tool_r1","tool_call":{"readToolCall":{"args":{"path":"/x.md"},"result":{"success":{"content":"hello","path":"/x.md","fileSize":5}}}}}`,
			wantJSON: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool_r1","name":"Read","input":{"path":"/x.md"}},{"type":"tool_result","tool_use_id":"tool_r1","content":"{\"content\":\"hello\",\"path\":\"/x.md\",\"fileSize\":5}"}]}}`,
		},
		{
			name:     "tool_call completed (shell) maps to Bash",
			input:    `{"type":"tool_call","subtype":"completed","call_id":"tool_s1","tool_call":{"shellToolCall":{"args":{"command":"ls"},"result":{"success":{"command":"ls","exitCode":0,"stdout":"a\nb","stderr":""}}}}}`,
			wantJSON: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool_s1","name":"Bash","input":{"command":"ls"}},{"type":"tool_result","tool_use_id":"tool_s1","content":"{\"command\":\"ls\",\"exitCode\":0,\"stdout\":\"a\\nb\",\"stderr\":\"\"}"}]}}`,
		},
		{
			name:     "tool_call completed with failure result",
			input:    `{"type":"tool_call","subtype":"completed","call_id":"tool_s2","tool_call":{"shellToolCall":{"args":{"command":"bad"},"result":{"failure":{"command":"bad","exitCode":127,"stderr":"not found"}}}}}`,
			wantJSON: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool_s2","name":"Bash","input":{"command":"bad"}},{"type":"tool_result","tool_use_id":"tool_s2","content":"{\"command\":\"bad\",\"exitCode\":127,\"stderr\":\"not found\"}"}]}}`,
		},
		{
			name:     "tool_call completed with error result",
			input:    `{"type":"tool_call","subtype":"completed","call_id":"tool_a1","tool_call":{"awaitToolCall":{"args":{},"result":{"error":{"error":"No shell found for id 45563"}}}}}`,
			wantJSON: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"tool_a1","name":"Await","input":{}},{"type":"tool_result","tool_use_id":"tool_a1","content":"{\"error\":\"No shell found for id 45563\"}"}]}}`,
		},
		{
			name:     "thinking delta becomes assistant thinking block",
			input:    `{"type":"thinking","subtype":"delta","text":"Let me think","session_id":"abc","timestamp_ms":1}`,
			wantJSON: `{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Let me think"}]}}`,
		},
		{
			name:    "thinking completed is dropped",
			input:   `{"type":"thinking","subtype":"completed","session_id":"abc","timestamp_ms":1}`,
			wantNil: true,
		},
		{
			name:    "thinking delta with empty text is dropped",
			input:   `{"type":"thinking","subtype":"delta","text":"","session_id":"abc","timestamp_ms":1}`,
			wantNil: true,
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
