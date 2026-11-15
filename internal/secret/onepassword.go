package secret

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// DefaultOPTokenSecret is the name of the agmux secret (keychain backend)
// that holds the 1Password service account token.
const DefaultOPTokenSecret = "OP_SERVICE_ACCOUNT_TOKEN"

// opTokenEnv is the environment variable `op` reads the service account
// token from.
const opTokenEnv = "OP_SERVICE_ACCOUNT_TOKEN"

// opTimeout bounds every `op` invocation so that a stuck biometric prompt
// (desktop app integration) can never hang the caller forever.
const opTimeout = 30 * time.Second

// opCommonPaths are searched when `op` is not in PATH (launchd's PATH is
// minimal).
var opCommonPaths = []string{"/opt/homebrew/bin/op", "/usr/local/bin/op"}

// opRefRe matches op://vault/item/field and op://vault/item/section/field.
// Segments may not contain whitespace or '?'/'#'.
var opRefRe = regexp.MustCompile(`^op://[^/\s?#]+/[^/\s?#]+/[^/\s?#]+(/[^/\s?#]+)?$`)

// ValidateOPRef checks that ref looks like an op:// secret reference.
func ValidateOPRef(ref string) error {
	if !opRefRe.MatchString(ref) {
		return errors.New("invalid 1Password reference: want op://vault/item/field")
	}
	return nil
}

// OPRunner runs the op CLI. Abstracted for tests.
type OPRunner interface {
	// Run executes op with args and the given full environment. It returns
	// stdout, stderr and the exit code. err is non-nil only when op could
	// not run to completion (not installed, timeout, ...).
	Run(ctx context.Context, env []string, args ...string) (stdout, stderr []byte, exitCode int, err error)
}

// errOPNotInstalled is returned by runners when the op binary is missing.
var errOPNotInstalled = errors.New("1Password CLI (op) is not installed; install it (brew install 1password-cli) or set [secret] op_path in the agmux config")

type opExecRunner struct {
	path    string
	timeout time.Duration
}

// NewOPRunner returns an OPRunner that executes the op binary. path may be
// empty, in which case PATH and common install locations are searched.
func NewOPRunner(path string) OPRunner {
	return &opExecRunner{path: path, timeout: opTimeout}
}

func (r *opExecRunner) locate() (string, error) {
	if r.path != "" {
		if _, err := os.Stat(r.path); err != nil {
			return "", fmt.Errorf("%w (configured path %s not found)", errOPNotInstalled, r.path)
		}
		return r.path, nil
	}
	if p, err := exec.LookPath("op"); err == nil {
		return p, nil
	}
	for _, p := range opCommonPaths {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errOPNotInstalled
}

func (r *opExecRunner) Run(ctx context.Context, env []string, args ...string) ([]byte, []byte, int, error) {
	path, err := r.locate()
	if err != nil {
		return nil, nil, -1, err
	}
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = 2 * time.Second
	err = cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, nil, -1, fmt.Errorf("op timed out after %s (a biometric prompt may be waiting; use a service account token: register it with `agmux secret add %s`)", r.timeout, DefaultOPTokenSecret)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return stdout.Bytes(), stderr.Bytes(), exitErr.ExitCode(), nil
	}
	if err != nil {
		return nil, nil, -1, fmt.Errorf("run op: %w", err)
	}
	return stdout.Bytes(), stderr.Bytes(), 0, nil
}

// TokenSource returns the 1Password service account token. found is false
// when no token is registered (the inherited environment is then used).
type TokenSource func(ctx context.Context) (token string, found bool, err error)

// KeychainTokenSource reads the service account token from the secret called
// name in b.
func KeychainTokenSource(b Backend, name string) TokenSource {
	return func(ctx context.Context) (string, bool, error) {
		v, err := b.Get(ctx, &Secret{Name: name})
		if errors.Is(err, ErrNotFound) {
			return "", false, nil
		}
		if err != nil {
			return "", false, fmt.Errorf("read 1Password service account token (%s): %w", name, err)
		}
		return v, v != "", nil
	}
}

// OnePasswordBackend resolves op:// references with `op read`. It stores
// nothing itself: agmux keeps only the reference in the secret's metadata.
type OnePasswordBackend struct {
	runner OPRunner
	token  TokenSource
	env    func() []string
}

// NewOnePasswordBackend returns a 1Password backend. token may be nil (always
// use the inherited environment).
func NewOnePasswordBackend(runner OPRunner, token TokenSource) *OnePasswordBackend {
	return &OnePasswordBackend{runner: runner, token: token, env: os.Environ}
}

func (b *OnePasswordBackend) Kind() BackendKind { return Backend1Password }

// Set is rejected: 1Password secrets only hold a reference.
func (b *OnePasswordBackend) Set(ctx context.Context, name, value string) error {
	return errors.New("the 1password backend does not store values; register an op:// reference instead (agmux secret add NAME --op op://vault/item/field)")
}

// Delete is a no-op: nothing is stored on the 1Password side.
func (b *OnePasswordBackend) Delete(ctx context.Context, name string) error { return nil }

func (b *OnePasswordBackend) Get(ctx context.Context, s *Secret) (string, error) {
	if err := ValidateOPRef(s.Ref); err != nil {
		return "", err
	}
	env := b.env()
	token := ""
	if b.token != nil {
		t, found, err := b.token(ctx)
		if err != nil {
			return "", err
		}
		if found {
			token = t
			filtered := make([]string, 0, len(env)+1)
			for _, kv := range env {
				if !strings.HasPrefix(kv, opTokenEnv+"=") {
					filtered = append(filtered, kv)
				}
			}
			env = append(filtered, opTokenEnv+"="+token)
		}
	}
	hasToken := token != "" || envHas(env, opTokenEnv)

	stdout, stderr, code, err := b.runner.Run(ctx, env, "read", "--no-newline", s.Ref)
	if err != nil {
		return "", errors.New(sanitizeOPText(err.Error(), token))
	}
	if code != 0 {
		return "", classifyOPError(code, string(stderr), token, hasToken)
	}
	return string(stdout), nil
}

func envHas(env []string, key string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") && len(kv) > len(key)+1 {
			return true
		}
	}
	return false
}

// sanitizeOPText removes the token and bounds the length.
func sanitizeOPText(s, token string) string {
	if token != "" {
		s = strings.ReplaceAll(s, token, "***")
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func classifyOPError(code int, stderr, token string, hasToken bool) error {
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "isn't an item") || strings.Contains(low, "isn't a vault") ||
		strings.Contains(low, "could not be found") || strings.Contains(low, "no item") ||
		strings.Contains(low, "isn't a field") || strings.Contains(low, "not found"):
		return errors.New("1Password reference not found (check the vault, item and field of the op:// reference, and that the service account can access the vault)")
	case strings.Contains(low, "authenticat") || strings.Contains(low, "service account token") ||
		strings.Contains(low, "unauthorized") || strings.Contains(low, "not signed in") ||
		strings.Contains(low, "sign in") || strings.Contains(low, "invalid token"):
		if !hasToken {
			return fmt.Errorf("1Password authentication failed and no service account token is registered; run `agmux secret add %s` with a service account token", DefaultOPTokenSecret)
		}
		return errors.New("1Password authentication failed: the service account token is missing, invalid or revoked; re-register it with `agmux secret add " + DefaultOPTokenSecret + "`")
	}
	msg := sanitizeOPText(stderr, token)
	if msg == "" {
		return fmt.Errorf("op read failed (exit %d)", code)
	}
	return fmt.Errorf("op read failed (exit %d): %s", code, msg)
}
