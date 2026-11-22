package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/myuon/agmux/internal/config"
)

// errServerUnreachable is returned by daemonPost when nothing is listening on
// the daemon's address (connection refused). Callers may fall back to an
// in-process session manager in that case only.
var errServerUnreachable = errors.New("agmux server is not running")

// daemonBaseURL returns the base URL of the running agmux daemon. Inside an
// agent session AGMUX_API_URL points at the daemon; otherwise use the
// configured port.
func daemonBaseURL(cfg *config.Config) string {
	if base := strings.TrimRight(os.Getenv("AGMUX_API_URL"), "/"); base != "" {
		return base
	}
	return fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
}

// daemonPost POSTs a JSON body to the daemon and decodes the JSON response
// into out (if non-nil). Non-2xx responses are returned as errors carrying the
// daemon's error message.
func daemonPost(baseURL, path string, body, out interface{}) error {
	return daemonRequest(http.MethodPost, baseURL, path, body, out)
}

// daemonRequest sends a JSON request with the given method to the daemon.
func daemonRequest(method, baseURL, path string, body, out interface{}) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	req, err := http.NewRequest(method, baseURL+path, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return errServerUnreachable
		}
		return fmt.Errorf("request %s %s failed: %w", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(data, &errResp); err == nil && errResp.Error != "" {
			return errors.New(errResp.Error)
		}
		return fmt.Errorf("API returned status %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// sendViaDaemon delivers text to a session through the running daemon, which
// owns the live connection to the session's holder. If the session is in the
// middle of a turn the daemon writes the message to the CLI's stdin and the
// CLI queues it as the next input; nothing is restarted.
// renameViaDaemon renames a session through the running daemon so the Web UI
// is notified of the change.
func renameViaDaemon(baseURL, id, name string) error {
	return daemonRequest(http.MethodPatch, baseURL, "/api/sessions/"+id, map[string]string{"name": name}, nil)
}

func sendViaDaemon(baseURL, id, text string) error {
	return daemonPost(baseURL, "/api/sessions/"+id+"/send", map[string]string{"text": text}, nil)
}

type daemonBroadcastResult struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
	Error     string `json:"error,omitempty"`
}

// broadcastViaDaemon asks the daemon to send text to every session matching filter.
func broadcastViaDaemon(baseURL, text, filter string) ([]daemonBroadcastResult, error) {
	var out struct {
		Results []daemonBroadcastResult `json:"results"`
	}
	err := daemonPost(baseURL, "/api/sessions/broadcast", map[string]string{"text": text, "filter": filter}, &out)
	return out.Results, err
}

// daemonCreateRequest mirrors the daemon's POST /api/sessions body
// (server.createSessionRequest).
type daemonCreateRequest struct {
	Name                    string `json:"name"`
	ProjectPath             string `json:"projectPath"`
	Prompt                  string `json:"prompt,omitempty"`
	Worktree                bool   `json:"worktree,omitempty"`
	Provider                string `json:"provider,omitempty"`
	Model                   string `json:"model,omitempty"`
	AutoApprove             bool   `json:"autoApprove,omitempty"`
	SystemPrompt            string `json:"systemPrompt,omitempty"`
	ParentSessionID         string `json:"parentSessionId,omitempty"`
	RoleTemplate            string `json:"roleTemplate,omitempty"`
	Ephemeral               bool   `json:"ephemeral,omitempty"`
	EphemeralTimeoutSeconds *int   `json:"ephemeralTimeoutSeconds,omitempty"`
	CreatedBySessionID      string `json:"createdBySessionId,omitempty"`
}

// daemonSessionRef is the part of the daemon's session JSON the CLI prints.
type daemonSessionRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// createViaDaemon creates a session through the running daemon. Whoever
// creates a session is the one that reads its holder's stream (result event ->
// idle, ephemeral timeout, auto-title). An in-process Manager in the
// short-lived CLI exits right after Create, so nobody saw the result event and
// the session stayed "working" until the daemon was restarted.
func createViaDaemon(baseURL string, req daemonCreateRequest) (daemonSessionRef, error) {
	var out daemonSessionRef
	err := daemonPost(baseURL, "/api/sessions", req, &out)
	return out, err
}

// forkViaDaemon forks a session through the running daemon, for the same
// reason as createViaDaemon.
func forkViaDaemon(baseURL, id string, preserveContext bool, prompt string) (daemonSessionRef, error) {
	var out daemonSessionRef
	body := map[string]interface{}{"preserveContext": preserveContext, "prompt": prompt}
	err := daemonPost(baseURL, "/api/sessions/"+id+"/fork", body, &out)
	return out, err
}
