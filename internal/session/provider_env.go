package session

import (
	"fmt"
	"strings"
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
