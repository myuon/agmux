package secret

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// KeychainService is the generic-password service name of agmux secrets.
// The account is the secret name.
const KeychainService = "agmux-secret"

// SecurityPath is the macOS security(1) tool. Every Keychain access goes
// through it (never through a Go keychain library) so that the item ACL
// trusts this stable, Apple-signed binary: reading never triggers a GUI
// permission dialog, even after `agmux update` replaces the agmux binary.
const SecurityPath = "/usr/bin/security"

// keychainValuePrefix marks values stored base64-encoded. Encoding means the
// value never needs quoting in `security -i` input (no spaces, quotes or
// backslashes) and `find-generic-password -w` always prints printable ASCII
// (it switches to hex output for non-printable / non-ASCII bytes).
const keychainValuePrefix = "agmux-b64:"

// securityLineLimit is a conservative bound on a `security -i` command line.
// security(1) reads interactive commands into a fixed ~4 KiB buffer and
// treats the overflow as a NEW command (echoing it to stderr), so we must
// stay well under it.
const securityLineLimit = 3900

// MaxKeychainValueLength is the largest value the keychain backend accepts.
const MaxKeychainValueLength = 2048

// securityExitNotFound is security(1)'s exit status for errSecItemNotFound.
const securityExitNotFound = 44

// securityTimeout bounds every security(1) invocation. If anything ever made
// it wait for a GUI dialog we give up instead of hanging (the user may be on
// a phone and unable to click it).
const securityTimeout = 15 * time.Second

// CommandRunner runs the security tool. Abstracted for tests.
type CommandRunner interface {
	// Run executes security with args, feeding stdin. It returns stdout and
	// the exit code. err is non-nil only when the command could not run to
	// completion (not found, timeout, ...). stderr must be discarded: in
	// `-i` mode it can echo the command line.
	Run(ctx context.Context, stdin []byte, args ...string) (stdout []byte, exitCode int, err error)
}

type securityRunner struct {
	path    string
	timeout time.Duration
}

// NewSecurityRunner returns a CommandRunner that executes /usr/bin/security.
func NewSecurityRunner() CommandRunner {
	return &securityRunner{path: SecurityPath, timeout: securityTimeout}
}

func (r *securityRunner) Run(ctx context.Context, stdin []byte, args ...string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.path, args...)
	// security(1) locates the default (login) keychain through $HOME. Pin it
	// to the real home so an overridden $HOME cannot make it look for a
	// missing keychain (which can pop up a "keychain not found" dialog).
	cmd.Env = append(os.Environ(), "HOME="+realHomeDir())
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, -1, errors.New("security command timed out")
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.Bytes(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return nil, -1, fmt.Errorf("run security: %w", err)
	}
	return stdout.Bytes(), 0, nil
}

// KeychainBackend stores values as generic passwords in the login keychain
// (service KeychainService, account = secret name).
type KeychainBackend struct {
	runner  CommandRunner
	service string
}

// NewKeychainBackend returns a keychain backend using runner.
func NewKeychainBackend(runner CommandRunner) *KeychainBackend {
	return NewKeychainBackendWithService(runner, KeychainService)
}

// NewKeychainBackendWithService returns a keychain backend using a specific
// service name (see KeychainServiceFor).
func NewKeychainBackendWithService(runner CommandRunner, service string) *KeychainBackend {
	return &KeychainBackend{runner: runner, service: service}
}

// Service returns the keychain service name items are stored under.
func (b *KeychainBackend) Service() string { return b.service }

func (b *KeychainBackend) Kind() BackendKind { return BackendKeychain }

// addCommand builds the `security -i` input line that stores value. The value
// is base64-encoded so it never needs escaping, and it travels via stdin so
// it never appears in any process's argv.
func (b *KeychainBackend) addCommand(name, value string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	if len(value) > MaxKeychainValueLength {
		return "", fmt.Errorf("secret value is too long for the keychain backend (max %d bytes); use --backend file", MaxKeychainValueLength)
	}
	encoded := keychainValuePrefix + base64.StdEncoding.EncodeToString([]byte(value))
	line := fmt.Sprintf("add-generic-password -U -s %s -a %s -w %s\n", b.service, name, encoded)
	if len(line) > securityLineLimit {
		return "", errors.New("secret value is too long for the keychain backend; use --backend file")
	}
	return line, nil
}

func (b *KeychainBackend) Set(ctx context.Context, name, value string) error {
	line, err := b.addCommand(name, value)
	if err != nil {
		return err
	}
	_, code, err := b.runner.Run(ctx, []byte(line), "-i")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("security add-generic-password failed (exit %d)", code)
	}
	// security -i may report success for malformed input; verify by reading
	// the item back (no dialog: the item was created by security itself).
	got, err := b.Get(ctx, &Secret{Name: name})
	if err != nil {
		return fmt.Errorf("verify keychain item: %w", err)
	}
	if got != value {
		return errors.New("verify keychain item: stored value does not match")
	}
	return nil
}

func (b *KeychainBackend) Get(ctx context.Context, s *Secret) (string, error) {
	if err := ValidateName(s.Name); err != nil {
		return "", err
	}
	out, code, err := b.runner.Run(ctx, nil, "find-generic-password", "-s", b.service, "-a", s.Name, "-w")
	if err != nil {
		return "", err
	}
	if code == securityExitNotFound {
		return "", ErrNotFound
	}
	if code != 0 {
		return "", fmt.Errorf("security find-generic-password failed (exit %d)", code)
	}
	raw := strings.TrimSuffix(string(out), "\n")
	if !strings.HasPrefix(raw, keychainValuePrefix) {
		// Not written by agmux (e.g. created by hand); use as-is.
		return raw, nil
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, keychainValuePrefix))
	if err != nil {
		return "", errors.New("keychain item is corrupted (invalid encoding)")
	}
	return string(dec), nil
}

func (b *KeychainBackend) Delete(ctx context.Context, name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	_, code, err := b.runner.Run(ctx, nil, "delete-generic-password", "-s", b.service, "-a", name)
	if err != nil {
		return err
	}
	if code == securityExitNotFound {
		return ErrNotFound
	}
	if code != 0 {
		return fmt.Errorf("security delete-generic-password failed (exit %d)", code)
	}
	return nil
}
