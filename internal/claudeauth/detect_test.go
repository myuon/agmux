package claudeauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResultLineIndicatesLoginRequired(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"not logged in", `{"type":"result","subtype":"success","is_error":true,"result":"Not logged in · Please run /login"}`, true},
		{"401 status", `{"type":"result","subtype":"success","is_error":true,"api_error_status":401,"result":"Failed to authenticate. API Error: 401 Invalid authentication credentials"}`, true},
		{"authentication_error", `{"type":"result","subtype":"success","is_error":true,"result":"Failed to authenticate. API Error: 401 {\"type\":\"error\",\"error\":{\"type\":\"authentication_error\"}}"}`, true},
		{"oauth expired", `{"type":"result","subtype":"success","is_error":true,"result":"OAuth token has expired. Please obtain a new token"}`, true},
		{"oauth session expired", `{"type":"result","subtype":"success","is_error":true,"result":"Failed to authenticate: OAuth session expired and could not be refreshed"}`, true},
		{"invalid api key", `{"type":"result","is_error": true,"result":"Invalid API key · Please run /login"}`, true},

		// Not a lost login.
		{"rate limit", `{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"result":"You're out of extra usage · resets 12am (Asia/Tokyo)"}`, false},
		{"overloaded", `{"type":"result","subtype":"success","is_error":true,"result":"API Error: 529 {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}"}`, false},
		{"permission 403", `{"type":"result","subtype":"success","is_error":true,"api_error_status":403,"result":"Failed to authenticate. API Error: 403 permission_error"}`, false},

		// The markers written in the conversation must not trigger.
		{"successful answer quoting marker", `{"type":"result","subtype":"success","is_error":false,"result":"If you see 'Please run /login', run claude auth login."}`, false},
		{"user message", `{"type":"user","message":{"role":"user","content":"Not logged in · Please run /login と出た"},"is_error":true}`, false},
		{"assistant text", `{"type":"assistant","message":{"content":[{"type":"text","text":"authentication_error: Please run /login"}]}}`, false},
		{"tool result error", `{"type":"user","message":{"content":[{"type":"tool_result","is_error":true,"content":"Not logged in · Please run /login"}]}}`, false},
		{"garbage", `not json "is_error":true Please run /login`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := ResultLineIndicatesLoginRequired([]byte(c.line))
			assert.Equal(t, c.want, got)
		})
	}
}

func TestResultLineReason(t *testing.T) {
	ok, reason := ResultLineIndicatesLoginRequired([]byte(`{"type":"result","is_error":true,"result":"Not logged in · Please run /login"}`))
	assert.True(t, ok)
	assert.Equal(t, "Not logged in · Please run /login", reason)
}

func TestHolderLogIndicatesLoginRequired(t *testing.T) {
	start := "2026/11/18 10:00:00 INFO holder: CLI process started pid=1 sessionId=abc\n"

	ok, _ := HolderLogIndicatesLoginRequired(start + "Error: OAuth token has expired. Please run /login\n")
	assert.True(t, ok, "CLI stderr after the start marker")

	ok, _ = HolderLogIndicatesLoginRequired("Not logged in · Please run /login\n" + start + "some other error\n")
	assert.False(t, ok, "output of an earlier CLI run must not re-trigger")

	ok, _ = HolderLogIndicatesLoginRequired(start + `2026/11/18 10:00:01 WARN holder: oversized input line dropped text="Please run /login"` + "\n")
	assert.False(t, ok, "the holder's own log lines are ignored")

	ok, _ = HolderLogIndicatesLoginRequired("")
	assert.False(t, ok)
}

func TestExtractLoginURL(t *testing.T) {
	out := "Opening browser to sign in…\r\nIf the browser didn't open, visit: \x1b]8;;https://claude.com/cai/oauth/authorize?code=true&client_id=x&state=s\x07https://claude.com/cai/oauth/authorize?code=true&client_id=x&state=s\x1b]8;;\x07\r\nPaste code here if prompted > "
	assert.Equal(t, "https://claude.com/cai/oauth/authorize?code=true&client_id=x&state=s", ExtractLoginURL(out))
	assert.Equal(t, "", ExtractLoginURL("Opening browser to sign in…\r\n"))
}
