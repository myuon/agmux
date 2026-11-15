package secret

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
)

// Injection is one secret value exposed to the child as an env var.
type Injection struct {
	// Name is the secret name (used in the mask replacement).
	Name string
	// EnvName is the environment variable the value is exported as.
	EnvName string
	Value   string
}

// ExecOptions configures Exec.
type ExecOptions struct {
	// Args is the command and its arguments (Args[0] is looked up in PATH).
	Args []string
	// BaseEnv is the environment the child starts from (e.g. os.Environ()).
	BaseEnv []string
	Inject  []Injection
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	// ForwardSignals relays SIGINT/SIGTERM/SIGHUP/SIGQUIT/SIGUSR1/SIGUSR2/
	// SIGWINCH received by this process to the child.
	ForwardSignals bool
}

// forwardedSignals are relayed to the child by Exec.
var forwardedSignals = []os.Signal{
	syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT,
	syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGWINCH,
}

// buildEnv returns base with every injected EnvName set (overriding any
// existing entry).
func buildEnv(base []string, inject []Injection) []string {
	override := map[string]bool{}
	for _, in := range inject {
		override[in.EnvName] = true
	}
	env := make([]string, 0, len(base)+len(inject))
	for _, kv := range base {
		k, _, _ := strings.Cut(kv, "=")
		if override[k] {
			continue
		}
		env = append(env, kv)
	}
	for _, in := range inject {
		env = append(env, in.EnvName+"="+in.Value)
	}
	return env
}

// Exec runs opts.Args with the secrets injected into its environment.
// stdout/stderr are streamed through a Masker that replaces every injected
// value with ***(NAME). It returns the child's exit code (128+signal when
// the child was killed by a signal). err is non-nil only when the child
// could not be started; the exit code is then 127.
func Exec(opts ExecOptions) (int, error) {
	if len(opts.Args) == 0 {
		return 2, errors.New("no command given")
	}
	rules := make([]MaskRule, 0, len(opts.Inject))
	for _, in := range opts.Inject {
		rules = append(rules, MaskRule{Value: in.Value, Replacement: MaskReplacement(in.Name)})
	}
	stdout := NewMasker(opts.Stdout, rules)
	stderr := NewMasker(opts.Stderr, rules)

	cmd := exec.Command(opts.Args[0], opts.Args[1:]...)
	cmd.Env = buildEnv(opts.BaseEnv, opts.Inject)
	cmd.Stdin = opts.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	var sigCh chan os.Signal
	if opts.ForwardSignals {
		// Register before Start so no signal is lost (default action would
		// kill us and orphan the child).
		sigCh = make(chan os.Signal, 8)
		signal.Notify(sigCh, forwardedSignals...)
		defer signal.Stop(sigCh)
	}

	if err := cmd.Start(); err != nil {
		return 127, fmt.Errorf("start %s: %w", opts.Args[0], err)
	}

	done := make(chan struct{})
	if sigCh != nil {
		go func() {
			for {
				select {
				case sig := <-sigCh:
					_ = cmd.Process.Signal(sig)
				case <-done:
					return
				}
			}
		}()
	}

	waitErr := cmd.Wait()
	close(done)
	_ = stdout.Flush()
	_ = stderr.Flush()

	if waitErr == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal()), nil
		}
		return exitErr.ExitCode(), nil
	}
	return 1, waitErr
}
