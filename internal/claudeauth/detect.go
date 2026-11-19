// Package claudeauth detects that the Claude CLI has lost its login and lets
// the user log in again from the web UI (experimental feature
// "claude_relogin", see #832).
//
// Detection only looks at messages the CLI itself produces: an error result
// (is_error) in the stream, the exit message of the CLI process, and the
// CLI's stderr in the holder log. Text written by the user or the agent in the
// conversation never reaches these places, so it cannot trigger detection.
package claudeauth

import (
	"encoding/json"
	"regexp"
	"strings"
)

// FeatureName is the experimental flag that gates everything in this package.
const FeatureName = "claude_relogin"

// loginMarkers are lower-cased fragments the CLI prints when its login is
// gone. They were taken from real error results ("Not logged in · Please run
// /login", "Failed to authenticate. API Error: 401 ... authentication_error",
// "OAuth session expired and could not be refreshed").
var loginMarkers = []string{
	"please run /login",
	"not logged in",
	"claude auth login",
	"authentication_error",
	"oauth token has expired",
	"oauth session expired",
	"invalid api key",
	"invalid authentication credentials",
}

// TextIndicatesLoginRequired reports whether a CLI-produced error message
// says the login is gone. Callers must only pass text the CLI generated.
func TextIndicatesLoginRequired(s string) bool {
	if s == "" {
		return false
	}
	lower := strings.ToLower(s)
	for _, m := range loginMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

type resultEvent struct {
	Type           string          `json:"type"`
	IsError        bool            `json:"is_error"`
	APIErrorStatus *int            `json:"api_error_status"`
	Result         json.RawMessage `json:"result"`
}

// ResultLineIndicatesLoginRequired inspects one raw stream-json line. Only a
// "result" event with is_error=true counts: a successful result carries the
// assistant's final answer, which may quote any of the markers. It returns a
// short reason for display when the line indicates a lost login.
func ResultLineIndicatesLoginRequired(line []byte) (bool, string) {
	// Cheap pre-filter: almost every line is not an error result.
	if !strings.Contains(string(line), `"is_error":true`) && !strings.Contains(string(line), `"is_error": true`) {
		return false, ""
	}
	var ev resultEvent
	if json.Unmarshal(line, &ev) != nil || ev.Type != "result" || !ev.IsError {
		return false, ""
	}
	var text string
	_ = json.Unmarshal(ev.Result, &text)
	if ev.APIErrorStatus != nil && *ev.APIErrorStatus == 401 {
		return true, firstLine(text)
	}
	if TextIndicatesLoginRequired(text) {
		return true, firstLine(text)
	}
	return false, ""
}

// holderLogLine matches lines the holder itself writes via the standard log
// package ("2026/11/16 15:05:10 INFO holder: ..."). Those are agmux's own
// messages, not the CLI's stderr.
var holderLogLine = regexp.MustCompile(`^\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2} `)

// holderStartMarker is logged by the holder each time it starts the CLI. Only
// output after the last one belongs to the current CLI process; the log file
// is appended across restarts and older output must not re-trigger.
const holderStartMarker = "holder: CLI process started"

// HolderLogIndicatesLoginRequired looks at the CLI's stderr in a holder log
// (the part written since the CLI was last started) and reports a lost login.
func HolderLogIndicatesLoginRequired(log string) (bool, string) {
	if i := strings.LastIndex(log, holderStartMarker); i >= 0 {
		log = log[i:]
		if nl := strings.IndexByte(log, '\n'); nl >= 0 {
			log = log[nl+1:]
		} else {
			log = ""
		}
	}
	for _, l := range strings.Split(log, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || holderLogLine.MatchString(l) {
			continue
		}
		if TextIndicatesLoginRequired(l) {
			return true, firstLine(l)
		}
	}
	return false, ""
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}
