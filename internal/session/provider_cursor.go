package session

import (
	"encoding/json"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
)

// knownCursorToolKinds lists the tool kind keys that may appear in
// {"tool_call":{"<kind>":{...}}}. They are tried in order so that the chosen
// kind is deterministic even though Go map iteration is not.
var knownCursorToolKinds = []string{
	"readToolCall",
	"shellToolCall",
	"editToolCall",
	"globToolCall",
	"grepToolCall",
	"awaitToolCall",
	"listDirToolCall",
	"todoToolCall",
}

// CursorProvider implements Provider for Cursor Agent CLI (`agent` / `cursor-agent`).
//
// The Cursor Agent CLI emits stream-json events that are largely Claude-compatible:
//   - {"type":"system","subtype":"init","session_id":"<uuid>",...}
//   - {"type":"assistant","message":{...}}
//   - {"type":"user","message":{...}}
//   - {"type":"tool_call","subtype":"started/completed",...}
//   - {"type":"thinking","subtype":"delta/completed",...}
//   - {"type":"result","subtype":"success","is_error":false,"result":"..."}
//
// Phase 1 focuses on launching the CLI, parsing session_id/model, and passing
// most events through unchanged. tool_call and thinking events are converted
// into the Claude-compatible assistant message shape so the frontend can render
// them uniformly.
type CursorProvider struct {
	command string

	// thinkingMu guards thinkingBuffers.
	thinkingMu sync.Mutex
	// thinkingBuffers accumulates thinking/delta text per Cursor session_id.
	// A buffer is flushed (and removed) when thinking/completed arrives, or
	// when any non-thinking event is observed for that session_id, so that
	// the buffered thinking text is emitted before the interrupting event.
	thinkingBuffers map[string]*strings.Builder
}

// NewCursorProvider creates a CursorProvider with the given command.
// If command is empty, defaults to "agent".
func NewCursorProvider(command string) *CursorProvider {
	if command == "" {
		command = "agent"
	}
	return &CursorProvider{
		command:         command,
		thinkingBuffers: make(map[string]*strings.Builder),
	}
}

func (p *CursorProvider) Name() ProviderName {
	return ProviderCursor
}

// IsOneShot returns true because the Cursor Agent CLI exits after each prompt
// and must be re-spawned with --resume to continue the conversation.
func (p *CursorProvider) IsOneShot() bool {
	return true
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
//
// Returns a slice of zero or more output lines. The cursor provider buffers
// consecutive thinking/delta events and emits them as a single assistant
// message when thinking/completed arrives or when an intervening non-thinking
// event needs to be emitted first.
func (p *CursorProvider) NormalizeStreamLine(line []byte) [][]byte {
	var envelope struct {
		Type      string `json:"type"`
		Subtype   string `json:"subtype"`
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal(line, &envelope) != nil {
		// Non-JSON line: flush any buffered thinking (we don't know which
		// session it belongs to, so flush all) and then pass through.
		flushed := p.flushAllThinking()
		flushed = append(flushed, line)
		return flushed
	}

	switch envelope.Type {
	case "thinking":
		return p.handleThinking(line, envelope.Subtype, envelope.SessionID)
	case "tool_call":
		// Flush any buffered thinking for this session before emitting the
		// tool_call so ordering is preserved.
		out := p.flushThinking(envelope.SessionID)
		if normalized := p.normalizeToolCall(line, envelope.Subtype); normalized != nil {
			out = append(out, normalized)
		}
		return out
	default:
		// system, assistant, user, result, etc. are Claude-compatible.
		// Flush any buffered thinking for this session first so that thinking
		// text appears before the interrupting event.
		out := p.flushThinking(envelope.SessionID)
		out = append(out, line)
		return out
	}
}

// cursorToolKindToName maps the Cursor tool_call kind key (e.g. "readToolCall")
// to a human-readable Claude-style tool name (e.g. "Read").
func cursorToolKindToName(kind string) string {
	switch kind {
	case "readToolCall":
		return "Read"
	case "shellToolCall":
		return "Bash"
	case "editToolCall":
		return "Edit"
	case "globToolCall":
		return "Glob"
	case "grepToolCall":
		return "Grep"
	case "awaitToolCall":
		return "Await"
	case "listDirToolCall":
		return "ListDir"
	case "todoToolCall":
		return "Todo"
	}
	// Fallback: strip "ToolCall" suffix and Title-case the first rune.
	name := strings.TrimSuffix(kind, "ToolCall")
	if name == "" {
		return "tool"
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func (p *CursorProvider) normalizeToolCall(line []byte, subtype string) []byte {
	// Drop in-progress tool_call events; only emit something on completion.
	if subtype != "completed" {
		return nil
	}

	// Cursor format:
	//   {"type":"tool_call","subtype":"completed",
	//    "call_id":"tool_xxx",
	//    "tool_call":{"<kind>ToolCall":{"args":{...},"result":{...}}},
	//    ...}
	var msg struct {
		Type     string                     `json:"type"`
		Subtype  string                     `json:"subtype"`
		CallID   string                     `json:"call_id"`
		ToolCall map[string]json.RawMessage `json:"tool_call"`
	}
	if json.Unmarshal(line, &msg) != nil {
		return line
	}

	// Find the kind key. Map iteration order in Go is non-deterministic, so we
	// first try known kinds in a fixed order, then fall back to any remaining
	// key (using sorted order for determinism) for forward-compatibility with
	// new Cursor tool kinds.
	var kind string
	var inner json.RawMessage
	for _, known := range knownCursorToolKinds {
		if v, ok := msg.ToolCall[known]; ok {
			kind = known
			inner = v
			break
		}
	}
	if kind == "" {
		keys := make([]string, 0, len(msg.ToolCall))
		for k := range msg.ToolCall {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			kind = keys[0]
			inner = msg.ToolCall[kind]
		}
	}
	if kind == "" {
		// Unknown shape — fall back to pass-through to avoid losing data.
		return line
	}

	var innerObj struct {
		Args   json.RawMessage `json:"args"`
		Result json.RawMessage `json:"result"`
	}
	_ = json.Unmarshal(inner, &innerObj)

	name := cursorToolKindToName(kind)

	type toolUseBlock struct {
		Type  string          `json:"type"`
		ID    string          `json:"id,omitempty"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input,omitempty"`
	}
	type toolResultBlock struct {
		Type      string `json:"type"`
		ToolUseID string `json:"tool_use_id,omitempty"`
		Content   string `json:"content"`
	}

	args := innerObj.Args
	if len(args) == 0 {
		// Always emit an object so the frontend can render an empty input.
		args = json.RawMessage(`{}`)
	}
	args = cursorToolArgsToClaudeInput(name, args)

	tu := toolUseBlock{Type: "tool_use", ID: msg.CallID, Name: name, Input: args}

	resultStr := cursorExtractToolResult(innerObj.Result)
	tr := toolResultBlock{Type: "tool_result", ToolUseID: msg.CallID, Content: resultStr}

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

// cursorToolArgsToClaudeInput converts Cursor tool args into the
// Claude-compatible tool_use input shape. Cursor's read/edit/write tools put
// the target file in args.path, while the Claude format (which the frontend
// renders) expects input.file_path. The key is renamed for those tools so the
// frontend shows the file path next to the tool name; other tools pass
// through unchanged.
func cursorToolArgsToClaudeInput(name string, args json.RawMessage) json.RawMessage {
	switch name {
	case "Read", "Edit", "Write":
	default:
		return args
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(args, &obj) != nil {
		return args
	}
	path, ok := obj["path"]
	if !ok {
		return args
	}
	if _, exists := obj["file_path"]; !exists {
		obj["file_path"] = path
	}
	delete(obj, "path")
	b, err := json.Marshal(obj)
	if err != nil {
		return args
	}
	return b
}

// cursorExtractToolResult unwraps the Cursor result envelope. Cursor results
// look like:
//
//	{"success": {...payload...}}
//	{"failure": {...payload...}}
//	{"error":   {...payload...} | "string"}
//
// It returns the inner payload serialized for display in tool_result.content.
// Strings are returned unquoted; other shapes are returned as compact JSON.
func cursorExtractToolResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return jsonRawToString(raw)
	}
	for _, key := range []string{"success", "failure", "error"} {
		if inner, ok := obj[key]; ok && len(inner) > 0 {
			return jsonRawToString(inner)
		}
	}
	return jsonRawToString(raw)
}

// handleThinking buffers Cursor's incremental thinking deltas and emits a
// single Claude-compatible assistant message when the run of deltas ends
// (either via thinking/completed or because another event for the same
// session is about to be emitted).
//
// Cursor emits:
//
//	{"type":"thinking","subtype":"delta","text":"...",...}
//	{"type":"thinking","subtype":"completed",...}
//
// The combined text is emitted as
//
//	{"type":"assistant","message":{"role":"assistant",
//	  "content":[{"type":"thinking","thinking":"<concatenated text>"}]}}
//
// so the frontend's StreamDisplayItem("thinking") rendering picks it up.
//
// NOTE: In practice the Cursor CLI does not currently emit a standalone
// thinking/completed event — flushes are driven almost entirely by the
// arrival of the next non-thinking event (e.g. an assistant message or
// tool_call) for the same session. The completed branch below is kept so
// that we behave correctly if/when Cursor starts emitting it.
func (p *CursorProvider) handleThinking(line []byte, subtype, sessionID string) [][]byte {
	switch subtype {
	case "delta":
		var msg struct {
			Text string `json:"text"`
		}
		if json.Unmarshal(line, &msg) != nil {
			// Malformed delta — keep raw line for visibility.
			out := p.flushThinking(sessionID)
			out = append(out, line)
			return out
		}
		if msg.Text == "" {
			return nil
		}
		p.thinkingMu.Lock()
		buf, ok := p.thinkingBuffers[sessionID]
		if !ok {
			buf = &strings.Builder{}
			p.thinkingBuffers[sessionID] = buf
		}
		buf.WriteString(msg.Text)
		p.thinkingMu.Unlock()
		return nil
	case "completed":
		return p.flushThinking(sessionID)
	default:
		// Unknown thinking subtype — flush and drop.
		return p.flushThinking(sessionID)
	}
}

// flushThinking emits the buffered thinking text for the given session_id (if
// any) as a single assistant message and clears the buffer. Returns the
// produced output lines (zero or one).
func (p *CursorProvider) flushThinking(sessionID string) [][]byte {
	p.thinkingMu.Lock()
	buf, ok := p.thinkingBuffers[sessionID]
	if !ok || buf.Len() == 0 {
		if ok {
			delete(p.thinkingBuffers, sessionID)
		}
		p.thinkingMu.Unlock()
		return nil
	}
	text := buf.String()
	delete(p.thinkingBuffers, sessionID)
	p.thinkingMu.Unlock()
	return [][]byte{buildCursorThinkingMessage(text)}
}

// ResetBuffers drops any accumulated thinking text for the given session ID
// without emitting it. Intended to be called after replaying historical
// JSONL through NormalizeStreamLine (e.g. loadExistingLines) so that a
// partially-buffered turn whose `completed` event never arrived does not
// leak into the live stream that follows.
//
// If sessionID is empty, all buffered sessions are dropped.
func (p *CursorProvider) ResetBuffers(sessionID string) {
	p.thinkingMu.Lock()
	defer p.thinkingMu.Unlock()
	if sessionID == "" {
		p.thinkingBuffers = make(map[string]*strings.Builder)
		return
	}
	delete(p.thinkingBuffers, sessionID)
}

// flushAllThinking emits every buffered session's accumulated thinking text
// as separate assistant messages, then clears all buffers. Used as a
// defensive fallback when we can't determine which session a line belongs to
// (e.g. unparseable JSON).
func (p *CursorProvider) flushAllThinking() [][]byte {
	p.thinkingMu.Lock()
	if len(p.thinkingBuffers) == 0 {
		p.thinkingMu.Unlock()
		return nil
	}
	keys := make([]string, 0, len(p.thinkingBuffers))
	for k := range p.thinkingBuffers {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic ordering
	out := make([][]byte, 0, len(keys))
	for _, k := range keys {
		buf := p.thinkingBuffers[k]
		if buf.Len() > 0 {
			out = append(out, buildCursorThinkingMessage(buf.String()))
		}
		delete(p.thinkingBuffers, k)
	}
	p.thinkingMu.Unlock()
	return out
}

func buildCursorThinkingMessage(text string) []byte {
	type thinkingBlock struct {
		Type     string `json:"type"`
		Thinking string `json:"thinking"`
	}
	tb := thinkingBlock{Type: "thinking", Thinking: text}
	tbJSON, _ := json.Marshal(tb)

	wrapper := struct {
		Type    string `json:"type"`
		Message struct {
			Role    string            `json:"role"`
			Content []json.RawMessage `json:"content"`
		} `json:"message"`
	}{Type: "assistant"}
	wrapper.Message.Role = "assistant"
	wrapper.Message.Content = []json.RawMessage{tbJSON}
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
