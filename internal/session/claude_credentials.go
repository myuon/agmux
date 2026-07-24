package session

// Claude Code keeps its OAuth credentials in two places on macOS: the login
// Keychain and ~/.claude/.credentials.json. The Keychain wins whenever it can
// be read, and the file is consulted only when that read fails.
//
// A Keychain entry holding an already expired token therefore wedges every
// CLI process that can read it: the CLI decides the session is expired,
// retries the refresh with the equally stale refresh token, and gives up with
// "Failed to authenticate: OAuth session expired and could not be refreshed"
// without ever reaching the API.
//
// agmux runs as a launchd agent, and processes in that lineage can read the
// Keychain secret — while a CLI started from an ordinary shell cannot, so it
// falls back to the still-valid file. That asymmetry is why the same command
// succeeds by hand and fails under agmux. See #701.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"time"
)

// claudeKeychainService is the generic-password service name Claude Code
// stores its OAuth credentials under.
const claudeKeychainService = "Claude Code-credentials"

// claudeSecurityTimeout bounds each `security` invocation so a locked or
// otherwise unresponsive Keychain cannot stall a session spawn.
const claudeSecurityTimeout = 5 * time.Second

// claudeCredentials is the subset of Claude Code's credential payload agmux
// needs. ExpiresAt is a Unix timestamp in milliseconds, and is a pointer so an
// absent field stays distinguishable from the expiresAt=0 corruption this
// package exists to detect.
type claudeCredentials struct {
	ClaudeAiOauth struct {
		ExpiresAt *int64 `json:"expiresAt"`
	} `json:"claudeAiOauth"`
}

// errNoClaudeKeychainEntry reports that no Claude Code entry exists in the
// Keychain, which is the healthy state after a repair.
var errNoClaudeKeychainEntry = errors.New("no Claude Code keychain entry")

// Indirection points so tests can drive the repair logic without touching the
// real Keychain.
var (
	claudeKeychainRead   = readClaudeKeychainSecret
	claudeKeychainDelete = deleteClaudeKeychainSecret
	claudeCredsFilePath  = defaultClaudeCredsFilePath
)

// defaultClaudeCredsFilePath returns the path of Claude Code's on-disk
// credential fallback.
func defaultClaudeCredsFilePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", ".credentials.json"), nil
}

// claudeKeychainAccount returns the account name Claude Code files its
// Keychain entry under, which is the current username.
func claudeKeychainAccount() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}

// runSecurity invokes the macOS `security` tool, retrying without the account
// filter when the account-qualified lookup finds nothing. Older entries were
// written with a different account, and matching on the service alone still
// identifies the item unambiguously.
func runSecurity(args ...string) ([]byte, error) {
	account := claudeKeychainAccount()

	withAccount := append([]string{args[0], "-a", account}, args[1:]...)
	out, err := runSecurityOnce(withAccount)
	if err == nil {
		return out, nil
	}

	return runSecurityOnce(args)
}

func runSecurityOnce(args []string) ([]byte, error) {
	ctxCmd := exec.Command("security", args...)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = ctxCmd.Output()
		close(done)
	}()
	select {
	case <-done:
		return out, err
	case <-time.After(claudeSecurityTimeout):
		_ = ctxCmd.Process.Kill()
		<-done
		return nil, fmt.Errorf("security timed out after %s", claudeSecurityTimeout)
	}
}

// readClaudeKeychainSecret returns the raw credential JSON stored in the
// Keychain, or errNoClaudeKeychainEntry when there is nothing to read.
func readClaudeKeychainSecret() ([]byte, error) {
	out, err := runSecurity("find-generic-password", "-w", "-s", claudeKeychainService)
	if err != nil {
		return nil, errNoClaudeKeychainEntry
	}
	return out, nil
}

// deleteClaudeKeychainSecret removes the Claude Code entry from the Keychain.
func deleteClaudeKeychainSecret() error {
	if _, err := runSecurity("delete-generic-password", "-s", claudeKeychainService); err != nil {
		return fmt.Errorf("delete keychain entry: %w", err)
	}
	return nil
}

// parseClaudeExpiry extracts the OAuth expiry from a credential payload.
//
// It reports ok=false when the payload carries no expiry field at all. An
// explicit expiresAt of 0 is instead reported as a valid — and long past —
// timestamp, because that is precisely the corruption being detected. Keeping
// the two apart matters: an unrecognized payload must never be mistaken for
// an expired one, or agmux would delete a Keychain entry it cannot judge.
func parseClaudeExpiry(payload []byte) (time.Time, bool) {
	var creds claudeCredentials
	if err := json.Unmarshal(payload, &creds); err != nil {
		return time.Time{}, false
	}
	if creds.ClaudeAiOauth.ExpiresAt == nil {
		return time.Time{}, false
	}
	return time.UnixMilli(*creds.ClaudeAiOauth.ExpiresAt), true
}

// ClaudeCredentialState describes what a repair attempt found.
type ClaudeCredentialState int

const (
	// ClaudeCredsHealthy means no stale Keychain entry is shadowing a valid
	// credential file.
	ClaudeCredsHealthy ClaudeCredentialState = iota
	// ClaudeCredsRepaired means an expired Keychain entry was removed so the
	// CLI falls back to the valid credential file.
	ClaudeCredsRepaired
	// ClaudeCredsUnrecoverable means the Keychain entry is expired and the
	// credential file cannot stand in for it, so only a re-login helps.
	ClaudeCredsUnrecoverable
)

// RepairClaudeCredentials removes a Keychain entry holding expired Claude Code
// OAuth credentials, but only when ~/.claude/.credentials.json still holds a
// token valid at now. That guard is what makes the deletion safe: agmux never
// discards the last working credential, so the worst case is that the CLI
// keeps using the file it would have used anyway.
//
// It is a no-op off macOS, where no Keychain exists.
func RepairClaudeCredentials(now time.Time) (ClaudeCredentialState, error) {
	if runtime.GOOS != "darwin" {
		return ClaudeCredsHealthy, nil
	}

	payload, err := claudeKeychainRead()
	if err != nil {
		// Nothing in the Keychain is the healthy post-repair state, and an
		// unreadable Keychain means the CLI will fall back to the file on its
		// own. Neither warrants an error.
		return ClaudeCredsHealthy, nil
	}

	keychainExpiry, ok := parseClaudeExpiry(payload)
	if !ok || keychainExpiry.After(now) {
		return ClaudeCredsHealthy, nil
	}

	// The Keychain entry is expired. Only remove it if the file can take over.
	path, err := claudeCredsFilePath()
	if err != nil {
		return ClaudeCredsUnrecoverable, fmt.Errorf("locate credentials file: %w", err)
	}
	fileBytes, err := os.ReadFile(path)
	if err != nil {
		return ClaudeCredsUnrecoverable, nil
	}
	fileExpiry, ok := parseClaudeExpiry(fileBytes)
	if !ok || !fileExpiry.After(now) {
		return ClaudeCredsUnrecoverable, nil
	}

	if err := claudeKeychainDelete(); err != nil {
		return ClaudeCredsUnrecoverable, err
	}
	return ClaudeCredsRepaired, nil
}

// DefaultCredentialWatchInterval is how often the daemon re-checks credential
// health. It is well under the CLI's own credential cache lifetime, so a
// Keychain entry that goes bad mid-session is cleared before a long-running
// process re-reads it.
const DefaultCredentialWatchInterval = 5 * time.Minute

// WatchClaudeCredentials repairs broken Claude Code credential storage on a
// timer until ctx is cancelled. The pre-spawn repair alone would leave a
// corrupted Keychain entry in place between spawns, where a session that is
// already running would still pick it up once its cached credentials lapse.
//
// Only state changes are logged, so a healthy daemon stays quiet.
func WatchClaudeCredentials(ctx context.Context, interval time.Duration) {
	if runtime.GOOS != "darwin" {
		return
	}
	if interval <= 0 {
		interval = DefaultCredentialWatchInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	last := ClaudeCredsHealthy
	for {
		state, err := RepairClaudeCredentials(time.Now())
		switch {
		case err != nil:
			slog.Warn("credential watch: repair failed", "component", "credential_watch", "error", err)
		case state == ClaudeCredsRepaired:
			slog.Info("credential watch: removed expired keychain credentials; CLI will use ~/.claude/.credentials.json", "component", "credential_watch")
		case state == ClaudeCredsUnrecoverable && last != ClaudeCredsUnrecoverable:
			slog.Warn("credential watch: keychain credentials are expired and ~/.claude/.credentials.json cannot replace them; run `claude` and re-login", "component", "credential_watch")
		}
		last = state

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
