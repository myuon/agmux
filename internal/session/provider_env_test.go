package session

import (
	"strings"
	"testing"
)

func envValues(env []string, key string) []string {
	var out []string
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			out = append(out, strings.TrimPrefix(kv, key+"="))
		}
	}
	return out
}

func TestProvidersSetAgmuxSessionEnv(t *testing.T) {
	// An inherited value (e.g. agmux itself running inside a session) must be
	// replaced, not duplicated.
	t.Setenv("AGMUX_SESSION_ID", "parent-session")
	opts := StreamOpts{SessionID: "sess-env", APIPort: 4321, ProjectPath: t.TempDir()}
	providers := map[string]Provider{
		"claude": &ClaudeProvider{},
		"cursor": &CursorProvider{command: "agent"},
		"codex":  &CodexProvider{command: "codex"},
	}
	for name, p := range providers {
		env := p.BuildStreamCommand(opts).Env
		if got := envValues(env, "AGMUX_SESSION_ID"); len(got) != 1 || got[0] != "sess-env" {
			t.Errorf("%s: AGMUX_SESSION_ID = %v, want [sess-env]", name, got)
		}
		if got := envValues(env, "AGMUX_API_URL"); len(got) != 1 || got[0] != "http://localhost:4321" {
			t.Errorf("%s: AGMUX_API_URL = %v", name, got)
		}
	}
}
