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
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Post(baseURL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) {
			return errServerUnreachable
		}
		return fmt.Errorf("request POST %s failed: %w", path, err)
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
