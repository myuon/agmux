// Package secret implements agmux's named secret store.
//
// Design:
//   - Metadata (name, description, backend kind, project scope, timestamps)
//     lives in the agmux SQLite DB (see Store). Values are NEVER stored there.
//   - Values live in a pluggable Backend (macOS Keychain via /usr/bin/security,
//     or one 0600 file per secret). Additional backends (e.g. 1Password, which
//     only stores a reference in Secret.Ref) plug in through the same interface.
//   - Values are only ever handed to child processes as environment variables
//     (see `agmux secret exec`); output is masked with Masker. There is
//     intentionally no API or CLI that prints a value.
package secret

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// BackendKind identifies where a secret's value is stored.
type BackendKind string

const (
	// BackendKeychain stores the value in the macOS login keychain.
	BackendKeychain BackendKind = "keychain"
	// BackendFile stores the value in ~/.agmux/secrets/<NAME> (0600).
	BackendFile BackendKind = "file"
	// Backend1Password resolves an op:// reference stored in Secret.Ref with
	// the 1Password CLI. agmux stores no value.
	Backend1Password BackendKind = "1password"
)

// DefaultBackend is used when neither the request nor the config selects one.
const DefaultBackend = BackendKeychain

// ErrNotFound is returned when a secret (metadata or value) does not exist.
var ErrNotFound = errors.New("secret not found")

// Secret is the metadata of a registered secret. It deliberately has no
// field for the value.
type Secret struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Backend     BackendKind `json:"backend"`
	// Ref is a backend-specific reference (e.g. an op:// URI for 1Password).
	// Empty for backends that store the value themselves.
	Ref string `json:"ref,omitempty"`
	// AllowedProjects restricts which project paths may use the secret.
	// Empty means every project is allowed.
	AllowedProjects []string   `json:"allowedProjects"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
	LastUsedAt      *time.Time `json:"lastUsedAt,omitempty"`
}

var nameRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// MaxNameLength bounds secret names (they become env var names and keychain
// account names).
const MaxNameLength = 128

// ValidateName checks that name is usable as an environment variable name:
// [A-Z_][A-Z0-9_]*.
func ValidateName(name string) error {
	if name == "" {
		return errors.New("secret name is required")
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("secret name is too long (max %d)", MaxNameLength)
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid secret name %q: must match [A-Z_][A-Z0-9_]*", name)
	}
	return nil
}

// ValidateBackendKind returns an error for unknown backend kinds.
func ValidateBackendKind(kind BackendKind) error {
	switch kind {
	case BackendKeychain, BackendFile, Backend1Password:
		return nil
	default:
		return fmt.Errorf("unknown backend %q (want keychain, file or 1password)", kind)
	}
}

// ProjectAllowed reports whether project may use a secret scoped to allowed.
// An empty allow-list permits every project. A project matches an entry when
// it is the same directory or a subdirectory of it (so worktrees under a
// project directory inherit the project's scope).
func ProjectAllowed(allowed []string, project string) bool {
	if len(allowed) == 0 {
		return true
	}
	if project == "" {
		return false
	}
	p := resolvePath(project)
	for _, a := range allowed {
		if a == "" {
			continue
		}
		a = resolvePath(a)
		if p == a || strings.HasPrefix(p, strings.TrimSuffix(a, string(filepath.Separator))+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// resolvePath cleans p and resolves symlinks when possible (best effort), so
// e.g. /var/... and /private/var/... compare equal on macOS.
func resolvePath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
