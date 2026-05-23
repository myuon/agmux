package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
)

// CursorProvider implements Provider for Cursor Agent CLI (`agent` / `cursor-agent`).
//
// The Cursor Agent CLI emits stream-json events that are largely Claude-compatible:
//   - {"type":"system","subtype":"init","session_id":"<uuid>",...}
//   - {"type":"assistant","message":{...}}
//   - {"type":"user","message":{...}}
//   - {"type":"tool_call","subtype":"started/completed",...}
//   - {"type":"result","subtype":"success","is_error":false,"result":"..."}
//
// Phase 1 focuses on launching the CLI, parsing session_id/model, and passing
// most events through unchanged. tool_call events are converted into the
// Claude-compatible tool_use / tool_result shape so the frontend can render
// them uniformly.
type CursorProvider struct {
	command string
}

// NewCursorProvider creates a CursorProvider with the given command.
// If command is empty, defaults to "agent".
func NewCursorProvider(command string) *CursorProvider {
	if command == "" {
		command = "agent"
	}
	return &CursorProvider{command: command}
}

func (p *CursorProvider) Name() ProviderName {
	return ProviderCursor
}

func (p *CursorProvider) BuildStreamCommand(opts StreamOpts) *exec.Cmd {
	args := []string{
		"-p",
		"--output-format", "stream-json",
		"--trust",
		"--force",
	}

	if opts.Resume && opts.CLISessionID != "" {
		args = append(args, "--resume", opts.CLISessionID)
	}

	if opts.ProjectPath != "" {
		args = append(args, "--workspace", opts.ProjectPath)
	}

	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}

	// Build the prompt to pass as the last positional argument.
	// Unlike Codex, Cursor has no stdin code path, so we never inject a dummy
	// placeholder for empty prompts — we simply omit the positional arg. The
	// only callers that reach here with an empty prompt are the ones that
	// resume an existing session (where the prompt is added by the resume
	// branch above) or callers that genuinely don't want to send anything.
	prompt := opts.InitialPrompt
	if !(opts.Resume && opts.CLISessionID != "") && opts.SystemPrompt != "" && prompt != "" {
		prompt = opts.SystemPrompt + "\n\n" + prompt
	}
	if prompt != "" {
		args = append(args, prompt)
	}

	cmd := exec.Command(p.command, args...)
	cmd.Dir = opts.ProjectPath
	// Filter out CLAUDECODE env var to avoid nested session detection.
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	return cmd
}

func (p *CursorProvider) ParseSessionID(jsonlLine []byte) (string, bool) {
	// Cursor emits Claude-compatible system init events:
	// {"type":"system","subtype":"init","session_id":"<uuid>",...}
	var msg struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(jsonlLine, &msg) == nil && msg.Type == "system" && msg.SessionID != "" {
		return msg.SessionID, true
	}
	return "", false
}

func (p *CursorProvider) ParseModel(jsonlLine []byte) (string, bool) {
	// system init event: {"type":"system","subtype":"init","model":"sonnet-4",...}
	var sysMsg struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Model   string `json:"model"`
	}
	if json.Unmarshal(jsonlLine, &sysMsg) == nil && sysMsg.Type == "system" && sysMsg.Subtype == "init" && sysMsg.Model != "" {
		return sysMsg.Model, true
	}

	// assistant message: {"type":"assistant","message":{"model":"sonnet-4",...}}
	var assistantMsg struct {
		Type    string `json:"type"`
		Message struct {
			Model string `json:"model"`
		} `json:"message"`
	}
	if json.Unmarshal(jsonlLine, &assistantMsg) == nil && assistantMsg.Type == "assistant" && assistantMsg.Message.Model != "" {
		return assistantMsg.Message.Model, true
	}

	return "", false
}

func (p *CursorProvider) SetupMCP(sessionID string, port int) (string, error) {
	// Phase 1: no MCP integration for Cursor.
	return "", nil
}

func (p *CursorProvider) CleanupMCP(sessionID string) error {
	// Phase 1: no per-session MCP config file to clean up.
	return nil
}

func (p *CursorProvider) AppendOTelEnv(env []string, port int) []string {
	// Cursor Agent CLI does not support OTel yet.
	return env
}

// NormalizeStreamLine converts Cursor-specific events into Claude-compatible
// stream-json format. Most event types are already Claude-compatible and pass
// through unchanged.
func (p *CursorProvider) NormalizeStreamLine(line []byte) []byte {
	var envelope struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	}
	if json.Unmarshal(line, &envelope) != nil {
		return line
	}

	switch envelope.Type {
	case "tool_call":
		return p.normalizeToolCall(line, envelope.Subtype)
	default:
		// system, assistant, user, result, etc. are Claude-compatible.
		return line
	}
}

func (p *CursorProvider) normalizeToolCall(line []byte, subtype string) []byte {
	// Drop in-progress tool_call events; only emit something on completion.
	if subtype != "completed" {
		return nil
	}

	var msg struct {
		Type    string          `json:"type"`
		Subtype string          `json:"subtype"`
		Name    string          `json:"name"`
		Input   json.RawMessage `json:"input"`
		Result  json.RawMessage `json:"result"`
		Output  json.RawMessage `json:"output"`
	}
	if json.Unmarshal(line, &msg) != nil {
		return line
	}

	name := msg.Name
	if name == "" {
		name = "tool"
	}

	type toolUseBlock struct {
		Type  string          `json:"type"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input,omitempty"`
	}
	type toolResultBlock struct {
		Type    string `json:"type"`
		Content string `json:"content"`
	}

	tu := toolUseBlock{Type: "tool_use", Name: name, Input: msg.Input}

	// Prefer "result" then fall back to "output".
	resultRaw := msg.Result
	if len(resultRaw) == 0 {
		resultRaw = msg.Output
	}
	resultStr := jsonRawToString(resultRaw)
	tr := toolResultBlock{Type: "tool_result", Content: resultStr}

	tuJSON, _ := json.Marshal(tu)
	trJSON, _ := json.Marshal(tr)

	wrapper := struct {
		Type    string `json:"type"`
		Message struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}{Type: "assistant"}
	wrapper.Message.Role = "assistant"
	wrapper.Message.Content = []json.RawMessage{tuJSON, trJSON}
	b, _ := json.Marshal(wrapper)
	return b
}

// jsonRawToString converts a json.RawMessage to a string representation
// suitable for tool_result.content. Strings are unquoted; other types are
// serialized as compact JSON. Empty input returns "".
func jsonRawToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}
