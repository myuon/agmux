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
			outs := p.NormalizeStreamLine([]byte(tc.input))
			if len(outs) == 0 {
				t.Fatalf("expected normalized output, got none")
			}
			got := parse(t, outs[len(outs)-1])
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
	// Each test case is a single line processed in isolation; the test
	// constructs a fresh provider per case so buffered thinking from one case
	// doesn't leak into the next.
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
			// Single delta with no following completed or interrupt is
			// buffered and produces no immediate output.
			name:    "thinking delta is buffered (no output until flush)",
			input:   `{"type":"thinking","subtype":"delta","text":"Let me think","session_id":"abc","timestamp_ms":1}`,
			wantNil: true,
		},
		{
			// Without any prior buffered text, completed produces nothing.
			name:    "thinking completed with empty buffer is dropped",
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
			p := NewCursorProvider("")
			results := p.NormalizeStreamLine([]byte(tt.input))

			if tt.wantNil {
				if len(results) != 0 {
					t.Errorf("expected no output, got %d lines: %v", len(results), bytesSliceToStrings(results))
				}
				return
			}

			if len(results) != 1 {
				t.Fatalf("expected exactly 1 output line, got %d: %v", len(results), bytesSliceToStrings(results))
			}
			result := results[0]

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

func bytesSliceToStrings(bs [][]byte) []string {
	out := make([]string, len(bs))
	for i, b := range bs {
		out[i] = string(b)
	}
	return out
}

// TestCursorProvider_ThinkingBuffering covers the per-session buffering of
// thinking/delta events introduced for issue #650: many small deltas should
// be coalesced into a single assistant thinking message, and intervening
// non-thinking events should cause an in-progress buffer to be flushed first
// so ordering is preserved.
func TestCursorProvider_ThinkingBuffering(t *testing.T) {
	type assistantThinking struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content []struct {
				Type     string `json:"type"`
				Thinking string `json:"thinking"`
			} `json:"content"`
		} `json:"message"`
	}

	parseThinking := func(t *testing.T, raw []byte) string {
		t.Helper()
		var msg assistantThinking
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatalf("failed to parse thinking message: %v\n%s", err, string(raw))
		}
		if msg.Type != "assistant" {
			t.Fatalf("expected type=assistant, got %q", msg.Type)
		}
		if len(msg.Message.Content) != 1 || msg.Message.Content[0].Type != "thinking" {
			t.Fatalf("expected single thinking content block, got %+v", msg.Message.Content)
		}
		return msg.Message.Content[0].Thinking
	}

	t.Run("multiple deltas coalesce into one assistant message on completed", func(t *testing.T) {
		p := NewCursorProvider("")
		inputs := []string{
			`{"type":"thinking","subtype":"delta","text":"Hello ","session_id":"sess1"}`,
			`{"type":"thinking","subtype":"delta","text":"world","session_id":"sess1"}`,
			`{"type":"thinking","subtype":"delta","text":"!","session_id":"sess1"}`,
		}
		for _, in := range inputs {
			if out := p.NormalizeStreamLine([]byte(in)); len(out) != 0 {
				t.Fatalf("expected delta to be buffered (no output), got %d lines: %v", len(out), bytesSliceToStrings(out))
			}
		}

		completed := []byte(`{"type":"thinking","subtype":"completed","session_id":"sess1"}`)
		out := p.NormalizeStreamLine(completed)
		if len(out) != 1 {
			t.Fatalf("expected 1 flushed thinking message, got %d: %v", len(out), bytesSliceToStrings(out))
		}
		got := parseThinking(t, out[0])
		if got != "Hello world!" {
			t.Errorf("thinking text = %q, want %q", got, "Hello world!")
		}
	})

	t.Run("tool_call interrupt flushes buffered thinking before tool message", func(t *testing.T) {
		p := NewCursorProvider("")
		// First chunk of thinking
		buffered := []string{
			`{"type":"thinking","subtype":"delta","text":"think1-a ","session_id":"sess1"}`,
			`{"type":"thinking","subtype":"delta","text":"think1-b","session_id":"sess1"}`,
		}
		for _, in := range buffered {
			if out := p.NormalizeStreamLine([]byte(in)); len(out) != 0 {
				t.Fatalf("expected delta to be buffered, got %v", bytesSliceToStrings(out))
			}
		}

		// tool_call completed should flush thinking1 first, then emit the tool message.
		toolCompleted := []byte(`{"type":"tool_call","subtype":"completed","call_id":"tool_r1","session_id":"sess1","tool_call":{"readToolCall":{"args":{"path":"/x.md"},"result":{"success":{"content":"x"}}}}}`)
		out := p.NormalizeStreamLine(toolCompleted)
		if len(out) != 2 {
			t.Fatalf("expected 2 output lines (thinking flush + tool), got %d: %v", len(out), bytesSliceToStrings(out))
		}
		if got := parseThinking(t, out[0]); got != "think1-a think1-b" {
			t.Errorf("thinking flush text = %q, want %q", got, "think1-a think1-b")
		}
		// Second line should be the tool message (an assistant with tool_use/tool_result blocks).
		var env struct {
			Type    string `json:"type"`
			Message struct {
				Content []map[string]interface{} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(out[1], &env); err != nil {
			t.Fatalf("failed to parse tool message: %v\n%s", err, string(out[1]))
		}
		if env.Type != "assistant" || len(env.Message.Content) == 0 {
			t.Errorf("expected assistant tool message with content, got %s", string(out[1]))
		}
		if got, _ := env.Message.Content[0]["type"].(string); got != "tool_use" {
			t.Errorf("expected first block tool_use, got %q", got)
		}

		// Second chunk of thinking after the tool — should buffer fresh.
		buffered2 := []string{
			`{"type":"thinking","subtype":"delta","text":"think2-x ","session_id":"sess1"}`,
			`{"type":"thinking","subtype":"delta","text":"think2-y","session_id":"sess1"}`,
		}
		for _, in := range buffered2 {
			if out := p.NormalizeStreamLine([]byte(in)); len(out) != 0 {
				t.Fatalf("expected delta to be buffered, got %v", bytesSliceToStrings(out))
			}
		}

		completed := p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"completed","session_id":"sess1"}`))
		if len(completed) != 1 {
			t.Fatalf("expected 1 flushed thinking message, got %d: %v", len(completed), bytesSliceToStrings(completed))
		}
		if got := parseThinking(t, completed[0]); got != "think2-x think2-y" {
			t.Errorf("second thinking text = %q, want %q", got, "think2-x think2-y")
		}
	})

	t.Run("assistant text interrupt flushes buffered thinking first", func(t *testing.T) {
		p := NewCursorProvider("")
		if out := p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"delta","text":"pre","session_id":"s1"}`)); len(out) != 0 {
			t.Fatalf("expected buffered, got %v", bytesSliceToStrings(out))
		}
		assistantLine := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]},"session_id":"s1"}`
		out := p.NormalizeStreamLine([]byte(assistantLine))
		if len(out) != 2 {
			t.Fatalf("expected thinking flush + assistant passthrough, got %d: %v", len(out), bytesSliceToStrings(out))
		}
		if got := parseThinking(t, out[0]); got != "pre" {
			t.Errorf("thinking flush = %q, want %q", got, "pre")
		}
		if string(out[1]) != assistantLine {
			t.Errorf("assistant line not passed through unchanged\ngot:  %s\nwant: %s", string(out[1]), assistantLine)
		}
	})

	t.Run("buffers are isolated per session_id", func(t *testing.T) {
		p := NewCursorProvider("")
		_ = p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"delta","text":"A","session_id":"sA"}`))
		_ = p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"delta","text":"B","session_id":"sB"}`))

		outA := p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"completed","session_id":"sA"}`))
		if len(outA) != 1 {
			t.Fatalf("session A: expected 1 flushed message, got %d", len(outA))
		}
		if got := parseThinking(t, outA[0]); got != "A" {
			t.Errorf("session A thinking = %q, want %q", got, "A")
		}

		outB := p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"completed","session_id":"sB"}`))
		if len(outB) != 1 {
			t.Fatalf("session B: expected 1 flushed message, got %d", len(outB))
		}
		if got := parseThinking(t, outB[0]); got != "B" {
			t.Errorf("session B thinking = %q, want %q", got, "B")
		}
	})

	// Covers the edge case where a session ends without ever emitting a
	// thinking delta: nothing is buffered, and a follow-up flush (or
	// ResetBuffers) is a no-op.
	t.Run("session with no thinking delta has empty buffer", func(t *testing.T) {
		p := NewCursorProvider("")
		// Drive a system + assistant + result pipeline without any thinking.
		_ = p.NormalizeStreamLine([]byte(`{"type":"system","subtype":"init","session_id":"sX","model":"sonnet-4"}`))
		_ = p.NormalizeStreamLine([]byte(`{"type":"assistant","message":{"role":"assistant","model":"sonnet-4","content":[{"type":"text","text":"ok"}]},"session_id":"sX"}`))
		_ = p.NormalizeStreamLine([]byte(`{"type":"result","subtype":"success","is_error":false,"result":"done","session_id":"sX"}`))

		p.thinkingMu.Lock()
		_, hasBuf := p.thinkingBuffers["sX"]
		bufCount := len(p.thinkingBuffers)
		p.thinkingMu.Unlock()
		if hasBuf {
			t.Errorf("expected no thinking buffer for sX, but one exists")
		}
		if bufCount != 0 {
			t.Errorf("expected no thinking buffers at all, got %d", bufCount)
		}

		// A completed event with nothing buffered should produce no output.
		out := p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"completed","session_id":"sX"}`))
		if len(out) != 0 {
			t.Errorf("expected no output for completed-with-empty-buffer, got %d", len(out))
		}

		// ResetBuffers on a session that never buffered should also be a
		// safe no-op.
		p.ResetBuffers("sX")
		p.thinkingMu.Lock()
		bufCount = len(p.thinkingBuffers)
		p.thinkingMu.Unlock()
		if bufCount != 0 {
			t.Errorf("expected no buffers after ResetBuffers, got %d", bufCount)
		}
	})
}

func TestCursorProvider_ResetBuffers(t *testing.T) {
	t.Run("drops partially buffered thinking without emitting it", func(t *testing.T) {
		p := NewCursorProvider("")

		// Accumulate some thinking text but never reach `completed`.
		_ = p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"delta","text":"half-","session_id":"sR"}`))
		_ = p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"delta","text":"baked","session_id":"sR"}`))

		// Sanity check: buffer is populated.
		p.thinkingMu.Lock()
		buf, ok := p.thinkingBuffers["sR"]
		p.thinkingMu.Unlock()
		if !ok || buf == nil || buf.Len() == 0 {
			t.Fatalf("precondition: expected non-empty buffer for sR")
		}

		// Reset and verify buffer is gone.
		p.ResetBuffers("sR")
		p.thinkingMu.Lock()
		_, stillThere := p.thinkingBuffers["sR"]
		p.thinkingMu.Unlock()
		if stillThere {
			t.Errorf("ResetBuffers should have removed sR's buffer")
		}

		// A subsequent flush attempt produces nothing (delta dropped, not emitted).
		out := p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"completed","session_id":"sR"}`))
		if len(out) != 0 {
			t.Errorf("expected no flushed output after ResetBuffers, got %d: %v", len(out), bytesSliceToStrings(out))
		}

		// And the next live event for this session must NOT carry leaked thinking text.
		live := p.NormalizeStreamLine([]byte(`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"fresh"}]},"session_id":"sR"}`))
		if len(live) != 1 {
			t.Fatalf("expected single passthrough output, got %d: %v", len(live), bytesSliceToStrings(live))
		}
		// The output should be the assistant line itself, not a thinking message.
		if strings.Contains(string(live[0]), `"thinking"`) {
			t.Errorf("ResetBuffers leaked thinking into live stream: %s", string(live[0]))
		}
	})

	t.Run("only drops the targeted session", func(t *testing.T) {
		p := NewCursorProvider("")

		_ = p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"delta","text":"keep","session_id":"sKeep"}`))
		_ = p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"delta","text":"drop","session_id":"sDrop"}`))

		p.ResetBuffers("sDrop")

		p.thinkingMu.Lock()
		_, keepOK := p.thinkingBuffers["sKeep"]
		_, dropOK := p.thinkingBuffers["sDrop"]
		p.thinkingMu.Unlock()
		if !keepOK {
			t.Errorf("ResetBuffers should not have touched sKeep's buffer")
		}
		if dropOK {
			t.Errorf("ResetBuffers should have removed sDrop's buffer")
		}
	})

	t.Run("empty sessionID drops all buffers", func(t *testing.T) {
		p := NewCursorProvider("")

		_ = p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"delta","text":"a","session_id":"s1"}`))
		_ = p.NormalizeStreamLine([]byte(`{"type":"thinking","subtype":"delta","text":"b","session_id":"s2"}`))

		p.ResetBuffers("")

		p.thinkingMu.Lock()
		count := len(p.thinkingBuffers)
		p.thinkingMu.Unlock()
		if count != 0 {
			t.Errorf("ResetBuffers(\"\") should drop all buffers, got %d remaining", count)
		}
	})

	t.Run("no-op on Claude and Codex providers", func(t *testing.T) {
		// Both should expose ResetBuffers and not panic.
		var _ Provider = NewClaudeProvider("", "")
		var _ Provider = NewCodexProvider("")
		NewClaudeProvider("", "").ResetBuffers("anything")
		NewCodexProvider("").ResetBuffers("anything")
	})
}
