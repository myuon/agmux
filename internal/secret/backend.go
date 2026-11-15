package secret

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
)

// Backend stores and retrieves secret values. Implementations must never log
// or embed values in returned errors.
type Backend interface {
	// Kind returns the backend identifier stored in Secret.Backend.
	Kind() BackendKind
	// Set stores (or replaces) the value for name.
	Set(ctx context.Context, name, value string) error
	// Get returns the value for s. Reference-style backends (e.g. 1Password)
	// can resolve s.Ref instead of s.Name. Returns ErrNotFound when missing.
	Get(ctx context.Context, s *Secret) (string, error)
	// Delete removes the value for name. Returns ErrNotFound when missing.
	Delete(ctx context.Context, name string) error
}

// Registry maps backend kinds to implementations.
type Registry map[BackendKind]Backend

// For returns the backend for kind.
func (r Registry) For(kind BackendKind) (Backend, error) {
	if err := ValidateBackendKind(kind); err != nil {
		return nil, err
	}
	b, ok := r[kind]
	if !ok {
		return nil, fmt.Errorf("backend %q is not available", kind)
	}
	return b, nil
}

// DefaultRegistry returns the standard backends: the macOS Keychain (through
// /usr/bin/security) and the file backend under ~/.agmux/secrets.
func DefaultRegistry() (Registry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	return Registry{
		BackendKeychain: NewKeychainBackend(NewSecurityRunner()),
		BackendFile:     NewFileBackend(filepath.Join(home, ".agmux", "secrets")),
	}, nil
}

// realHomeDir returns the login user's home directory from the user database
// rather than $HOME. Used for /usr/bin/security so it always finds the real
// login keychain even when $HOME is overridden.
func realHomeDir() string {
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		return u.HomeDir
	}
	h, _ := os.UserHomeDir()
	return h
}
