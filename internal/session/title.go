package session

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"
)

// Name sources stored in sessions.name_source (#770).
const (
	// NameSourceManual: the name was chosen explicitly (user, CLI, API caller,
	// duplicate/fork/etc.). Never auto-renamed.
	NameSourceManual = "manual"
	// NameSourceDefault: the Web UI default name was kept; the session is
	// waiting for its first user message to be auto-titled.
	NameSourceDefault = "default"
	// NameSourceGenerating: auto-title generation is in flight.
	NameSourceGenerating = "generating"
	// NameSourceAuto: the name was generated automatically.
	NameSourceAuto = "auto"
)

const (
	autoTitleTimeout      = 60 * time.Second
	autoTitleMaxInput     = 2000
	autoTitleMaxNameRunes = 40
)

// TitleGenerator produces a short session title for the user's first message.
type TitleGenerator func(ctx context.Context, message string) (string, error)

// SetTitleGenerator overrides the auto-title generator (used by tests).
func (m *Manager) SetTitleGenerator(fn TitleGenerator) {
	m.titleGenerator = fn
}

// SetOnNameChange sets a callback fired after a session's name changes
// (manual rename or auto-title).
func (m *Manager) SetOnNameChange(fn func(sessionID, name string)) {
	m.onNameChange = fn
}

func (m *Manager) fireNameChange(id, name string) {
	if m.onNameChange != nil {
		m.onNameChange(id, name)
	}
}

// Rename sets the session name explicitly. Once renamed manually, the session
// is never auto-titled again.
func (m *Manager) Rename(id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("name must not be empty")
	}
	res, err := m.db.Exec("UPDATE sessions SET name = ?, name_source = ?, updated_at = ? WHERE id = ?", name, NameSourceManual, time.Now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("session not found: %s", id)
	}
	m.fireNameChange(id, name)
	return nil
}

// claimAutoTitle atomically moves a session from "default" to "generating".
// It returns true only for the caller that won the claim, so a title is
// generated at most once per pending session even under concurrent sends.
func (m *Manager) claimAutoTitle(id string) bool {
	res, err := m.db.Exec("UPDATE sessions SET name_source = ? WHERE id = ? AND name_source = ?", NameSourceGenerating, id, NameSourceDefault)
	if err != nil {
		m.logger.Error("auto-title: claim failed", "sessionId", id, "error", err)
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// maybeStartAutoTitle generates a title asynchronously if the session is
// waiting for one. message is the user's first message.
func (m *Manager) maybeStartAutoTitle(id, message string) {
	if strings.TrimSpace(message) == "" {
		return
	}
	if !m.claimAutoTitle(id) {
		return
	}
	go m.runAutoTitle(id, message)
}

// runAutoTitle generates and stores a title for a session previously claimed
// with claimAutoTitle. On failure the name is left unchanged and the session
// returns to "default" so a later user message may retry.
func (m *Manager) runAutoTitle(id, message string) {
	gen := m.titleGenerator
	if gen == nil {
		gen = m.claudeTitleGenerator
	}
	ctx, cancel := context.WithTimeout(context.Background(), autoTitleTimeout)
	defer cancel()

	raw, err := gen(ctx, truncateRunes(message, autoTitleMaxInput))
	title := sanitizeTitle(raw)
	if err == nil && title == "" {
		err = fmt.Errorf("empty title")
	}
	if err != nil {
		m.logger.Warn("auto-title: generation failed; keeping current name", "sessionId", id, "error", err)
		_, _ = m.db.Exec("UPDATE sessions SET name_source = ? WHERE id = ? AND name_source = ?", NameSourceDefault, id, NameSourceGenerating)
		return
	}

	// Only write if still pending: a manual rename in the meantime wins.
	res, err := m.db.Exec("UPDATE sessions SET name = ?, name_source = ?, updated_at = ? WHERE id = ? AND name_source = ?",
		title, NameSourceAuto, time.Now(), id, NameSourceGenerating)
	if err != nil {
		m.logger.Error("auto-title: update failed", "sessionId", id, "error", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return
	}
	m.logger.Info("auto-title: session renamed", "sessionId", id, "name", title)
	m.fireNameChange(id, title)
}

const autoTitlePrompt = `Generate a concise title for a chat session, based on the user's first message below.
Rules:
- At most 30 characters.
- Use the same language as the message.
- Output only the title itself: no quotes, no trailing punctuation, no prefix like "Title:", no explanation.

<message>
%s
</message>`

// claudeTitleGenerator runs a one-shot `claude -p --model haiku` with no tools.
func (m *Manager) claudeTitleGenerator(ctx context.Context, message string) (string, error) {
	cmd := exec.CommandContext(ctx, m.claudeCommand,
		"-p",
		"--model", "haiku",
		"--output-format", "text",
		"--tools", "",
		"--strict-mcp-config",
		"--no-session-persistence",
	)
	cmd.Dir = os.TempDir()
	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, "CLAUDECODE=") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	cmd.Env = appendClaudeExtraEnv(cmd.Env)
	cmd.Stdin = strings.NewReader(fmt.Sprintf(autoTitlePrompt, message))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude title generation: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// sanitizeTitle normalizes generator output into a single-line title.
func sanitizeTitle(s string) string {
	s = strings.TrimSpace(s)
	// Use the first non-empty line only.
	for _, line := range strings.Split(s, "\n") {
		if l := strings.TrimSpace(line); l != "" {
			s = l
			break
		}
	}
	for _, p := range []string{"Title:", "title:", "タイトル:", "タイトル："} {
		s = strings.TrimSpace(strings.TrimPrefix(s, p))
	}
	for {
		prev := s
		s = strings.Trim(s, "\"'`「」『』*#“”‘’ ")
		s = strings.TrimRight(s, "。.、,!！?？:：")
		if s == prev {
			break
		}
	}
	s = strings.Join(strings.Fields(s), " ")
	return truncateRunes(s, autoTitleMaxNameRunes)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
