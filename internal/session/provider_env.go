package session

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// appendAgmuxSessionEnv sets AGMUX_SESSION_ID / AGMUX_API_URL for the agent
// process so tools it runs (the stdio MCP server, `agmux secret exec`, ...)
// can identify the session and reach this server. Values inherited from the
// parent environment are replaced.
func appendAgmuxSessionEnv(env []string, opts StreamOpts) []string {
	set := map[string]string{}
	if opts.SessionID != "" {
		set["AGMUX_SESSION_ID"] = opts.SessionID
	}
	if opts.APIPort > 0 {
		set["AGMUX_API_URL"] = fmt.Sprintf("http://localhost:%d", opts.APIPort)
	}
	if len(set) == 0 {
		return env
	}
	out := make([]string, 0, len(env)+len(set))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := set[k]; ok {
			continue
		}
		out = append(out, kv)
	}
	for _, k := range []string{"AGMUX_SESSION_ID", "AGMUX_API_URL"} {
		if v, ok := set[k]; ok {
			out = append(out, k+"="+v)
		}
	}
	return out
}

// ClaudeEnvSource returns extra environment variables for a claude CLI
// process (e.g. CLAUDE_CODE_OAUTH_TOKEN from the long-lived token, #833), or
// nil when there is nothing to add. Values are secrets: never log them.
type ClaudeEnvSource func() map[string]string

var (
	claudeEnvMu     sync.RWMutex
	claudeEnvSource ClaudeEnvSource
)

// SetClaudeEnvSource registers the source used whenever a claude session
// process is built (new sessions, holder restarts, automations, recovery).
// nil removes it.
func SetClaudeEnvSource(fn ClaudeEnvSource) {
	claudeEnvMu.Lock()
	claudeEnvSource = fn
	claudeEnvMu.Unlock()
}

// appendClaudeExtraEnv sets the variables from the registered source,
// replacing inherited values of the same name.
func appendClaudeExtraEnv(env []string) []string {
	claudeEnvMu.RLock()
	fn := claudeEnvSource
	claudeEnvMu.RUnlock()
	if fn == nil {
		return env
	}
	set := fn()
	if len(set) == 0 {
		return env
	}
	out := make([]string, 0, len(env)+len(set))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if _, ok := set[k]; ok {
			continue
		}
		out = append(out, kv)
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out = append(out, k+"="+set[k])
	}
	return out
}
