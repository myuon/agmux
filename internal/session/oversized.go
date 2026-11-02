package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	"github.com/myuon/agmux/internal/linereader"
)

// Oversized stream lines
//
// A single stdout line of the CLI can be arbitrarily large (e.g. a subagent
// reading a 13MB PDF yields a 16MB tool_result). Readers use linereader so
// that such a line never stops them. The holder additionally replaces the line
// by a small valid JSON line before persisting/broadcasting it, so that
//   - the stream file keeps exactly one line per original line (offsets and
//     line-count based cursors stay consistent),
//   - the UI shows a notice, and
//   - status detection is not wedged by the dropped content.
//
// What was dropped is guessed from the line's bytes with plain substring
// searches (see lineSniffer); it is not a JSON parser and is best effort.
//
// Replacement forms:
//
//	result            -> {"type":"result","subtype":<kept|"success">,"is_error":<kept|false>,
//	                      "result":<notice>,"session_id":..,"oversized_output":{...}}
//	                     (a minimal result: turn-complete detection and the UI keep working)
//	user tool_result  -> {"type":"user","message":{"role":"user","content":[{"type":"tool_result",
//	                      "tool_use_id":<kept>,"is_error":true,"content":<notice>}]},
//	                      "parent_tool_use_id":<kept|null>,"session_id":..,"oversized_output":{...}}
//	                     (only if a tool_use_id was found; the tool call stops spinning)
//	anything else     -> {"type":"system","subtype":"oversized_output","message":<notice>,
//	                      "parent_tool_use_id":<kept|null>,"session_id":..,"oversized_output":{...}}
//
// Limits: only the first tool_use_id of a line is recovered (a line holding
// several tool_results leaves the others spinning); a dropped system
// task_started/task_notification is not preserved as such (background task
// counters can drift); a dropped assistant tool_use is simply missing; if
// parent_tool_use_id/session_id are absent from the line they are null/omitted
// (a subagent line then shows up in the main timeline instead of its thread).

const (
	oversizedSubtype      = "oversized_output"
	oversizedInputSubtype = "oversized_input"
)

// lineSniffer extracts a few fields from an oversized line that is streamed
// through it chunk by chunk. Inside JSON string values a quote is always
// escaped, so an unescaped `"key":` sequence is a real object key; first
// occurrences are used, which for Claude's stream-json are the top-level
// fields (nested objects rarely reuse these keys).
type lineSniffer struct {
	carry   []byte
	started bool

	leadType      string
	sawResultType bool
	subtype       string
	hasIsError    bool
	isError       bool
	sessionID     string
	parentSet     bool
	parentToolUse string // "" with parentSet means null
	toolUseID     string
}

// sniffCarry is how many bytes of the previous chunk are re-examined so that
// a key/value split across chunks is still found.
const sniffCarry = 512

func (s *lineSniffer) reset() { *s = lineSniffer{} }

func (s *lineSniffer) observe(chunk []byte) {
	buf := chunk
	if len(s.carry) > 0 {
		buf = append(append(make([]byte, 0, len(s.carry)+len(chunk)), s.carry...), chunk...)
	}
	if !s.started {
		s.started = true
		if v, ok := stringAfter(buf, []byte(`{"type":`)); ok && bytes.HasPrefix(buf, []byte(`{"type":`)) {
			s.leadType = v
		}
	}
	if !s.sawResultType && bytes.Contains(buf, []byte(`"type":"result"`)) {
		s.sawResultType = true
	}
	if s.subtype == "" {
		if v, ok := stringAfter(buf, []byte(`"subtype":`)); ok {
			s.subtype = v
		}
	}
	if !s.hasIsError {
		if i := bytes.Index(buf, []byte(`"is_error":`)); i >= 0 {
			rest := buf[i+len(`"is_error":`):]
			switch {
			case bytes.HasPrefix(rest, []byte("true")):
				s.hasIsError, s.isError = true, true
			case bytes.HasPrefix(rest, []byte("false")):
				s.hasIsError = true
			}
		}
	}
	if s.sessionID == "" {
		if v, ok := stringAfter(buf, []byte(`"session_id":`)); ok {
			s.sessionID = v
		}
	}
	if !s.parentSet {
		if i := bytes.Index(buf, []byte(`"parent_tool_use_id":`)); i >= 0 {
			rest := buf[i+len(`"parent_tool_use_id":`):]
			if bytes.HasPrefix(rest, []byte("null")) {
				s.parentSet = true
			} else if v, ok := stringAfter(buf, []byte(`"parent_tool_use_id":`)); ok {
				s.parentSet, s.parentToolUse = true, v
			}
		}
	}
	if s.toolUseID == "" {
		if v, ok := stringAfter(buf, []byte(`"tool_use_id":`)); ok {
			s.toolUseID = v
		}
	}

	if len(buf) > sniffCarry {
		s.carry = append(s.carry[:0], buf[len(buf)-sniffCarry:]...)
	} else {
		s.carry = append(s.carry[:0], buf...)
	}
}

// stringAfter returns the short simple JSON string value following the first
// occurrence of key (e.g. `"session_id":`). It returns false if the key is
// absent, the value is not a string, or the string is not complete in buf.
func stringAfter(buf, key []byte) (string, bool) {
	i := bytes.Index(buf, key)
	if i < 0 {
		return "", false
	}
	rest := buf[i+len(key):]
	for len(rest) > 0 && rest[0] == ' ' {
		rest = rest[1:]
	}
	if len(rest) == 0 || rest[0] != '"' {
		return "", false
	}
	rest = rest[1:]
	for j := 0; j < len(rest) && j < 256; j++ {
		switch rest[j] {
		case '"':
			return string(rest[:j]), true
		case '\\':
			return "", false
		}
	}
	return "", false
}

// lineType is the best-effort type of the dropped line ("" if unknown).
func (s *lineSniffer) lineType() string {
	if s.leadType != "" {
		return s.leadType
	}
	if s.sawResultType {
		return "result"
	}
	return ""
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}

func oversizedNotice(size int64, typ string) string {
	if typ == "" {
		typ = "unknown"
	}
	return fmt.Sprintf("出力が大きすぎるため省略しました（%s, type=%s）", formatBytes(size), typ)
}

// buildOversizedReplacement builds the single-line replacement JSON for an
// oversized line of the given original size, using what the sniffer found.
func buildOversizedReplacement(size int64, s *lineSniffer) []byte {
	typ := s.lineType()
	notice := oversizedNotice(size, typ)
	meta := map[string]any{"bytes": size, "original_type": typ}
	var parent any
	if s.parentToolUse != "" {
		parent = s.parentToolUse
	}
	m := map[string]any{"oversized_output": meta}
	if s.sessionID != "" {
		m["session_id"] = s.sessionID
	}

	switch {
	case typ == "result":
		sub := s.subtype
		if sub == "" {
			sub = "success"
		}
		m["type"] = "result"
		m["subtype"] = sub
		m["is_error"] = s.hasIsError && s.isError
		m["result"] = notice
	case typ == "user" && s.toolUseID != "":
		m["type"] = "user"
		m["message"] = map[string]any{
			"role": "user",
			"content": []any{map[string]any{
				"type":        "tool_result",
				"tool_use_id": s.toolUseID,
				"is_error":    true,
				"content":     notice,
			}},
		}
		m["parent_tool_use_id"] = parent
	default:
		m["type"] = "system"
		m["subtype"] = oversizedSubtype
		m["message"] = notice
		m["parent_tool_use_id"] = parent
	}
	out, _ := json.Marshal(m)
	return out
}

// buildOversizedInputNotice is the stream line shown when a message sent to
// the CLI was too large and therefore NOT delivered.
func buildOversizedInputNotice(size int64) []byte {
	m := map[string]any{
		"type":             "system",
		"subtype":          oversizedInputSubtype,
		"message":          fmt.Sprintf("送信メッセージが大きすぎるため CLI に渡せませんでした（%s）", formatBytes(size)),
		"oversized_output": map[string]any{"bytes": size, "direction": "input"},
	}
	out, _ := json.Marshal(m)
	return out
}

// StreamLineReader reads stream JSONL (CLI stdout, stream files) line by line
// and turns oversized lines into their replacement line, so that callers see
// one valid line per original line and never stop at a long one.
type StreamLineReader struct {
	r         *linereader.Reader
	sn        lineSniffer
	sessionID string // for logging only
	source    string // for logging only
}

// NewStreamLineReader wraps r. sessionID and source are used only in logs.
func NewStreamLineReader(r io.Reader, sessionID, source string) *StreamLineReader {
	return newStreamLineReaderLimit(r, sessionID, source, 0)
}

// newStreamLineReaderLimit is NewStreamLineReader with a custom max line size
// (0 = default); used by tests.
func newStreamLineReaderLimit(r io.Reader, sessionID, source string, maxLine int) *StreamLineReader {
	s := &StreamLineReader{sessionID: sessionID, source: source}
	s.r = linereader.New(r, linereader.Options{MaxLine: maxLine, Observe: s.sn.observe})
	return s
}

// Next returns the next line (valid until the next call). For an oversized
// line it returns the replacement line and oversized=true, after logging a
// warning (size and detected type only, never content).
func (s *StreamLineReader) Next() (line []byte, oversized bool, err error) {
	s.sn.reset()
	l, err := s.r.Next()
	if err != nil {
		return nil, false, err
	}
	if !l.Oversized {
		return l.Data, false, nil
	}
	typ := s.sn.lineType()
	slog.Warn("oversized stream line replaced",
		"sessionId", s.sessionID, "source", s.source, "bytes", l.Size,
		"type", typ, "subtype", s.sn.subtype)
	return buildOversizedReplacement(l.Size, &s.sn), true, nil
}
