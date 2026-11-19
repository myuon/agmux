package claudeauth

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/creack/pty"
)

// CLIStarter returns a Starter that runs `<claudeCommand> auth login` in a PTY.
func CLIStarter(claudeCommand string) Starter {
	return cliStarter(claudeCommand, "auth", "login")
}

// CLISetupTokenStarter returns a Starter that runs `<claudeCommand> setup-token`
// in a PTY.
func CLISetupTokenStarter(claudeCommand string) Starter {
	return cliStarter(claudeCommand, "setup-token")
}

func cliStarter(claudeCommand string, args ...string) Starter {
	if claudeCommand == "" {
		claudeCommand = "claude"
	}
	return func() (Process, error) {
		cmd := exec.Command(claudeCommand, args...)
		cmd.Env = loginEnv()
		// A wide terminal keeps the URL on one line.
		f, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 40, Cols: 4000})
		if err != nil {
			return nil, err
		}
		return &ptyProcess{cmd: cmd, f: f}, nil
	}
}

// loginEnv is the daemon's environment without the variables that would make
// the CLI think it runs inside another Claude Code, and with BROWSER pointing
// at a no-op so nothing pops up on the host's screen: the user opens the URL
// on their own device.
func loginEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "CLAUDECODE" || k == "CLAUDE_CODE_ENTRYPOINT" || k == "BROWSER" || k == TokenSecretName {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "BROWSER=/usr/bin/true")
}

type ptyProcess struct {
	cmd      *exec.Cmd
	f        *os.File
	waitOnce sync.Once
	waitErr  error
}

func (p *ptyProcess) Read(b []byte) (int, error)  { return p.f.Read(b) }
func (p *ptyProcess) Write(b []byte) (int, error) { return p.f.Write(b) }

func (p *ptyProcess) Wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		_ = p.f.Close()
	})
	return p.waitErr
}

func (p *ptyProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// CLIStatusChecker returns a StatusChecker based on `claude auth status --json`.
func CLIStatusChecker(claudeCommand string) StatusChecker {
	if claudeCommand == "" {
		claudeCommand = "claude"
	}
	return func(ctx context.Context) (bool, error) {
		cmd := exec.CommandContext(ctx, claudeCommand, "auth", "status", "--json")
		cmd.Env = loginEnv()
		out, err := cmd.Output()
		// `auth status` exits non-zero when logged out; the JSON still says so.
		var st struct {
			LoggedIn bool `json:"loggedIn"`
		}
		if jerr := json.Unmarshal(out, &st); jerr != nil {
			if err != nil {
				return false, err
			}
			return false, jerr
		}
		return st.LoggedIn, nil
	}
}
