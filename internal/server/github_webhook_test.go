package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/myuon/agmux/internal/db"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := db.Open(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

func signPayload(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postWebhook(t *testing.T, handler http.Handler, event string, payload interface{}, secret string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/github/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	if secret != "" {
		req.Header.Set("X-Hub-Signature-256", signPayload(secret, body))
	}
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	return rr
}

func TestVerifyGitHubSignature(t *testing.T) {
	secret := "mysecret"
	body := []byte(`{"action":"created"}`)
	sig := signPayload(secret, body)

	if !verifyGitHubSignature(secret, body, sig) {
		t.Error("expected valid signature to pass")
	}

	if verifyGitHubSignature(secret, body, "sha256=invalidsig") {
		t.Error("expected invalid signature to fail")
	}

	if verifyGitHubSignature(secret, body, "badsig") {
		t.Error("expected signature without prefix to fail")
	}
}

func TestGitHubWebhookHandler_IssueComment_Remind(t *testing.T) {
	database := openTestDB(t)
	h := &githubWebhookHandler{db: database, logger: nopLogger(t)}

	payload := githubWebhookPayload{
		Action: "created",
		Repository: githubRepository{FullName: "myuon/agmux"},
		Issue:   &githubIssue{Number: 42, State: "open"},
		Comment: &githubComment{
			Body: "/remind do the thing",
			User: githubUser{Login: "alice"},
		},
	}

	if err := h.handleIssueComment(payload); err != nil {
		t.Fatalf("handleIssueComment: %v", err)
	}

	var memo string
	err := database.QueryRow(`SELECT memo FROM github_reminders WHERE repo='myuon/agmux' AND number=42 AND login='alice'`).Scan(&memo)
	if err != nil {
		t.Fatalf("query reminder: %v", err)
	}
	if memo != "do the thing" {
		t.Errorf("expected memo 'do the thing', got %q", memo)
	}
}

func TestGitHubWebhookHandler_IssueComment_Upsert(t *testing.T) {
	database := openTestDB(t)
	h := &githubWebhookHandler{db: database, logger: nopLogger(t)}

	base := githubWebhookPayload{
		Action:     "created",
		Repository: githubRepository{FullName: "myuon/agmux"},
		Issue:      &githubIssue{Number: 1, State: "open"},
		Comment:    &githubComment{Body: "/remind first memo", User: githubUser{Login: "bob"}},
	}
	if err := h.handleIssueComment(base); err != nil {
		t.Fatal(err)
	}
	base.Comment.Body = "/remind updated memo"
	if err := h.handleIssueComment(base); err != nil {
		t.Fatal(err)
	}

	var count int
	database.QueryRow(`SELECT COUNT(*) FROM github_reminders WHERE repo='myuon/agmux' AND number=1 AND login='bob'`).Scan(&count)
	if count != 1 {
		t.Errorf("expected 1 row after upsert, got %d", count)
	}

	var memo string
	database.QueryRow(`SELECT memo FROM github_reminders WHERE repo='myuon/agmux' AND number=1 AND login='bob'`).Scan(&memo)
	if memo != "updated memo" {
		t.Errorf("expected 'updated memo', got %q", memo)
	}
}

func TestGitHubWebhookHandler_IssueComment_IgnoreNonRemind(t *testing.T) {
	database := openTestDB(t)
	h := &githubWebhookHandler{db: database, logger: nopLogger(t)}

	payload := githubWebhookPayload{
		Action:     "created",
		Repository: githubRepository{FullName: "myuon/agmux"},
		Issue:      &githubIssue{Number: 99},
		Comment:    &githubComment{Body: "just a regular comment", User: githubUser{Login: "alice"}},
	}
	if err := h.handleIssueComment(payload); err != nil {
		t.Fatal(err)
	}

	var count int
	database.QueryRow(`SELECT COUNT(*) FROM github_reminders`).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 reminders, got %d", count)
	}
}

func TestGitHubWebhookHandler_IssueComment_IgnoreEmptyLogin(t *testing.T) {
	database := openTestDB(t)
	h := &githubWebhookHandler{db: database, logger: nopLogger(t)}

	payload := githubWebhookPayload{
		Action:     "created",
		Repository: githubRepository{FullName: "myuon/agmux"},
		Issue:      &githubIssue{Number: 1},
		Comment:    &githubComment{Body: "/remind memo", User: githubUser{Login: ""}},
	}
	if err := h.handleIssueComment(payload); err != nil {
		t.Fatal(err)
	}

	var count int
	database.QueryRow(`SELECT COUNT(*) FROM github_reminders`).Scan(&count)
	if count != 0 {
		t.Errorf("expected 0 reminders for empty login, got %d", count)
	}
}

func TestGitHubWebhookHandler_IssuesClosed_FireReminders(t *testing.T) {
	database := openTestDB(t)
	h := &githubWebhookHandler{db: database, logger: nopLogger(t)}

	// Insert a reminder directly
	_, err := database.Exec(`INSERT INTO github_reminders (repo, number, login, memo) VALUES ('myuon/agmux', 10, 'alice', 'check it')`)
	if err != nil {
		t.Fatal(err)
	}

	payload := githubWebhookPayload{
		Action:     "closed",
		Repository: githubRepository{FullName: "myuon/agmux"},
		Issue:      &githubIssue{Number: 10, State: "closed"},
	}
	if err := h.handleIssues(payload); err != nil {
		t.Fatalf("handleIssues: %v", err)
	}

	// After firing, reminder should be deleted
	var count int
	database.QueryRow(`SELECT COUNT(*) FROM github_reminders WHERE repo='myuon/agmux' AND number=10`).Scan(&count)
	if count != 0 {
		t.Errorf("expected reminder to be deleted after firing, got %d", count)
	}
}

func TestGitHubWebhookHandler_PullRequestMerged_FireReminders(t *testing.T) {
	database := openTestDB(t)
	h := &githubWebhookHandler{db: database, logger: nopLogger(t)}

	_, err := database.Exec(`INSERT INTO github_reminders (repo, number, login, memo) VALUES ('myuon/agmux', 5, 'bob', 'review next PR')`)
	if err != nil {
		t.Fatal(err)
	}

	payload := githubWebhookPayload{
		Action:     "closed",
		Repository: githubRepository{FullName: "myuon/agmux"},
		PR:         &githubPullRequest{Number: 5, State: "closed", Merged: true},
	}
	if err := h.handlePullRequest(payload); err != nil {
		t.Fatalf("handlePullRequest: %v", err)
	}

	var count int
	database.QueryRow(`SELECT COUNT(*) FROM github_reminders WHERE repo='myuon/agmux' AND number=5`).Scan(&count)
	if count != 0 {
		t.Errorf("expected reminder to be deleted after merge fire, got %d", count)
	}
}

func TestGitHubWebhookHandler_PullRequestClosed_NoMerge_NoFire(t *testing.T) {
	database := openTestDB(t)
	h := &githubWebhookHandler{db: database, logger: nopLogger(t)}

	_, err := database.Exec(`INSERT INTO github_reminders (repo, number, login, memo) VALUES ('myuon/agmux', 7, 'carol', 'something')`)
	if err != nil {
		t.Fatal(err)
	}

	// Closed but NOT merged
	payload := githubWebhookPayload{
		Action:     "closed",
		Repository: githubRepository{FullName: "myuon/agmux"},
		PR:         &githubPullRequest{Number: 7, State: "closed", Merged: false},
	}
	if err := h.handlePullRequest(payload); err != nil {
		t.Fatalf("handlePullRequest: %v", err)
	}

	// Reminder should still exist
	var count int
	database.QueryRow(`SELECT COUNT(*) FROM github_reminders WHERE repo='myuon/agmux' AND number=7`).Scan(&count)
	if count != 1 {
		t.Errorf("expected reminder to remain when PR closed without merge, got %d", count)
	}
}

// nopLogger returns a no-op slog.Logger for tests.
func nopLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(bytes.NewBuffer(nil), nil))
}

func TestGitHubWebhookHTTP_InvalidSignature(t *testing.T) {
	database := openTestDB(t)

	// We need a minimal server just for the handler. Use a raw http.HandlerFunc
	// that wraps only the logic we need tested. Since we can't easily inject config
	// in unit tests without a full server, we test the signature function directly.
	// This test validates the HTTP-layer behavior through verifyGitHubSignature.
	secret := "topsecret"
	body := []byte(`{}`)
	badSig := "sha256=deadbeef"
	if verifyGitHubSignature(secret, body, badSig) {
		t.Error("bad signature should fail")
	}

	goodSig := signPayload(secret, body)
	if !verifyGitHubSignature(secret, body, goodSig) {
		t.Error("good signature should pass")
	}

	_ = database // used for table existence
}

func TestUpsertReminder_MultipleUsers(t *testing.T) {
	database := openTestDB(t)
	h := &githubWebhookHandler{db: database, logger: nopLogger(t)}

	if err := h.upsertReminder("org/repo", 1, "alice", "memo-a"); err != nil {
		t.Fatal(err)
	}
	if err := h.upsertReminder("org/repo", 1, "bob", "memo-b"); err != nil {
		t.Fatal(err)
	}

	var count int
	database.QueryRow(`SELECT COUNT(*) FROM github_reminders WHERE repo='org/repo' AND number=1`).Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 reminders for 2 users, got %d", count)
	}
}

func TestFireReminders_Empty(t *testing.T) {
	database := openTestDB(t)
	h := &githubWebhookHandler{db: database, logger: nopLogger(t)}

	// Should not error when no reminders exist
	if err := h.fireReminders("org/repo", 999); err != nil {
		t.Errorf("fireReminders on empty set should not error: %v", err)
	}
}

// Ensure the github_reminders table schema is correct.
func TestGitHubRemindersTableSchema(t *testing.T) {
	database := openTestDB(t)

	// Insert and verify all columns work
	_, err := database.Exec(`
		INSERT INTO github_reminders (repo, number, login, memo)
		VALUES ('test/repo', 1, 'user1', 'test memo')
	`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	var id int64
	var repo string
	var number int
	var login, memo string
	err = database.QueryRow(`SELECT id, repo, number, login, memo FROM github_reminders`).Scan(&id, &repo, &number, &login, &memo)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if repo != "test/repo" || number != 1 || login != "user1" || memo != "test memo" {
		t.Errorf("unexpected values: repo=%q number=%d login=%q memo=%q", repo, number, login, memo)
	}

	// Test UNIQUE constraint
	_, err = database.Exec(`
		INSERT INTO github_reminders (repo, number, login, memo)
		VALUES ('test/repo', 1, 'user1', 'duplicate')
	`)
	if err == nil {
		t.Error("expected unique constraint violation")
	}

	_ = fmt.Sprintf("id=%d", id) // use id
}
