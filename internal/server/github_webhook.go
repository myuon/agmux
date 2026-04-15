package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/myuon/agmux/internal/config"
)

// githubWebhookPayload is the common top-level structure for GitHub webhook events.
type githubWebhookPayload struct {
	Action  string             `json:"action"`
	Issue   *githubIssue       `json:"issue"`
	Comment *githubComment     `json:"comment"`
	PR      *githubPullRequest `json:"pull_request"`

	// Repository info
	Repository githubRepository `json:"repository"`
}

type githubRepository struct {
	FullName string `json:"full_name"`
}

type githubIssue struct {
	Number int    `json:"number"`
	State  string `json:"state"`
}

type githubComment struct {
	Body string      `json:"body"`
	User githubUser  `json:"user"`
}

type githubUser struct {
	Login string `json:"login"`
}

type githubPullRequest struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Merged bool   `json:"merged"`
}

// handleGitHubWebhook handles POST /api/github/webhook
func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	if err != nil {
		s.logger.Error("github webhook: load config", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	secret := cfg.GitHub.WebhookSecret
	if secret == "" {
		s.logger.Warn("github webhook: webhook_secret is empty; skipping signature verification")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}

	if secret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if !verifyGitHubSignature(secret, body, sig) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}

	event := r.Header.Get("X-GitHub-Event")

	var payload githubWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "parse body", http.StatusBadRequest)
		return
	}

	handler := &githubWebhookHandler{
		db:     s.sqlDB,
		logger: s.logger,
	}

	switch event {
	case "issue_comment":
		if err := handler.handleIssueComment(payload); err != nil {
			s.logger.Error("github webhook: handle issue_comment", "err", err)
			http.Error(w, "handle issue_comment", http.StatusInternalServerError)
			return
		}
	case "issues":
		if err := handler.handleIssues(payload); err != nil {
			s.logger.Error("github webhook: handle issues", "err", err)
			http.Error(w, "handle issues", http.StatusInternalServerError)
			return
		}
	case "pull_request":
		if err := handler.handlePullRequest(payload); err != nil {
			s.logger.Error("github webhook: handle pull_request", "err", err)
			http.Error(w, "handle pull_request", http.StatusInternalServerError)
			return
		}
	default:
		// Ignore unknown events
	}

	w.WriteHeader(http.StatusNoContent)
}

// verifyGitHubSignature verifies the HMAC-SHA256 signature from GitHub.
func verifyGitHubSignature(secret string, body []byte, sig string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(sig, prefix) {
		return false
	}
	got, err := hex.DecodeString(sig[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(got, expected)
}

type githubWebhookHandler struct {
	db     *sql.DB
	logger *slog.Logger
}

// handleIssueComment processes issue_comment events and registers /remind commands.
func (h *githubWebhookHandler) handleIssueComment(payload githubWebhookPayload) error {
	if payload.Action != "created" {
		return nil
	}
	if payload.Comment == nil || payload.Issue == nil {
		return nil
	}

	// Guard against bot or empty login
	login := payload.Comment.User.Login
	if login == "" {
		return nil
	}

	body := strings.TrimSpace(payload.Comment.Body)
	if !strings.HasPrefix(body, "/remind ") {
		return nil
	}

	memo := strings.TrimSpace(strings.TrimPrefix(body, "/remind "))
	if memo == "" {
		return nil
	}

	repo := payload.Repository.FullName
	number := payload.Issue.Number

	if err := h.upsertReminder(repo, number, login, memo); err != nil {
		return fmt.Errorf("upsert reminder: %w", err)
	}

	h.logger.Info("github webhook: reminder registered",
		"repo", repo,
		"number", number,
		"login", login,
		"memo", memo,
	)
	return nil
}

// handleIssues processes issues events and fires reminders on close.
func (h *githubWebhookHandler) handleIssues(payload githubWebhookPayload) error {
	if payload.Action != "closed" {
		return nil
	}
	if payload.Issue == nil {
		return nil
	}

	repo := payload.Repository.FullName
	number := payload.Issue.Number

	return h.fireReminders(repo, number)
}

// handlePullRequest processes pull_request events and fires reminders on merge.
func (h *githubWebhookHandler) handlePullRequest(payload githubWebhookPayload) error {
	if payload.Action != "closed" {
		return nil
	}
	if payload.PR == nil {
		return nil
	}
	// Only fire on merged, not on plain close without merge
	if !payload.PR.Merged {
		return nil
	}

	repo := payload.Repository.FullName
	number := payload.PR.Number

	return h.fireReminders(repo, number)
}

// upsertReminder inserts or replaces a reminder for (repo, number, login).
func (h *githubWebhookHandler) upsertReminder(repo string, number int, login, memo string) error {
	_, err := h.db.Exec(`
		INSERT INTO github_reminders (repo, number, login, memo, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(repo, number, login) DO UPDATE SET
			memo = excluded.memo,
			updated_at = CURRENT_TIMESTAMP
	`, repo, number, login, memo)
	return err
}

// fireReminders fetches all reminders for (repo, number), logs them, and deletes them.
func (h *githubWebhookHandler) fireReminders(repo string, number int) error {
	rows, err := h.db.Query(`
		SELECT id, login, memo FROM github_reminders
		WHERE repo = ? AND number = ?
	`, repo, number)
	if err != nil {
		return fmt.Errorf("query reminders: %w", err)
	}
	defer rows.Close()

	type reminder struct {
		id    int64
		login string
		memo  string
	}
	var reminders []reminder
	for rows.Next() {
		var r reminder
		if err := rows.Scan(&r.id, &r.login, &r.memo); err != nil {
			return fmt.Errorf("scan reminder: %w", err)
		}
		reminders = append(reminders, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows iteration: %w", err)
	}

	for _, r := range reminders {
		h.logger.Info("github webhook: firing reminder",
			"repo", repo,
			"number", number,
			"login", r.login,
			"memo", r.memo,
		)

		_, err := h.db.Exec(`DELETE FROM github_reminders WHERE id = ?`, r.id)
		if err != nil {
			h.logger.Error("github webhook: delete reminder", "id", r.id, "err", err)
		}
	}

	return nil
}
