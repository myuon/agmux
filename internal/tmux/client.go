package tmux

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const SessionPrefix = "agmux-"

type TmuxSession struct {
	Name      string
	CreatedAt time.Time
}

type Client struct{}

func NewClient() *Client {
	return &Client{}
}

func (c *Client) NewSession(name string, workDir string, command string) error {
	sessionName := SessionPrefix + name
	args := []string{"new-session", "-d", "-s", sessionName, "-c", workDir, "-e", "CLAUDECODE="}
	if command != "" {
		args = append(args, command)
	}
	return c.run(args...)
}

func (c *Client) ListSessions() ([]TmuxSession, error) {
	out, err := c.output("list-sessions", "-F", "#{session_name}:#{session_created}")
	if err != nil {
		if strings.Contains(err.Error(), "no server running") || strings.Contains(string(out), "no server running") {
			return nil, nil
		}
		return nil, err
	}

	var sessions []TmuxSession
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := parts[0]
		if !strings.HasPrefix(name, SessionPrefix) {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, parts[1])
		sessions = append(sessions, TmuxSession{
			Name:      name,
			CreatedAt: ts,
		})
	}
	return sessions, nil
}

func (c *Client) KillSession(name string) error {
	sessionName := SessionPrefix + name
	return c.run("kill-session", "-t", sessionName)
}

// SendKeys sends text + Enter, then an extra Enter after 1s for Claude Code TUI submission.
func (c *Client) SendKeys(sessionName string, keys string) error {
	if err := c.run("send-keys", "-t", sessionName, keys, "Enter"); err != nil {
		return err
	}
	time.Sleep(1 * time.Second)
	return c.run("send-keys", "-t", sessionName, "Enter")
}

// SendKeysOnce sends text + Enter (single). Use for shell commands, not Claude TUI input.
func (c *Client) SendKeysOnce(sessionName string, keys string) error {
	return c.run("send-keys", "-t", sessionName, keys, "Enter")
}

// SendKeysRaw sends keys without Enter.
func (c *Client) SendKeysRaw(sessionName string, keys string) error {
	return c.run("send-keys", "-t", sessionName, keys)
}

func (c *Client) CapturePane(sessionName string, lines int) (string, error) {
	out, err := c.output("capture-pane", "-t", sessionName, "-p", "-S", fmt.Sprintf("-%d", lines))
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (c *Client) HasSession(name string) bool {
	sessionName := SessionPrefix + name
	err := c.run("has-session", "-t", sessionName)
	return err == nil
}

func (c *Client) HasSessionByFullName(sessionName string) bool {
	err := c.run("has-session", "-t", sessionName)
	return err == nil
}

func (c *Client) run(args ...string) error {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux %s: %w: %s", strings.Join(args, " "), err, string(out))
	}
	return nil
}

func (c *Client) output(args ...string) ([]byte, error) {
	cmd := exec.Command("tmux", args...)
	return cmd.CombinedOutput()
}
