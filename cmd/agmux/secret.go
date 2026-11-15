package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/myuon/agmux/internal/config"
	"github.com/myuon/agmux/internal/secret"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// secretAPI talks to the daemon's /api/secrets endpoints. Note that no
// endpoint returns a secret value: `secret exec` reads values directly from
// the backend (Keychain via /usr/bin/security, or file) in this process.
type secretAPI struct {
	baseURL string
	client  *http.Client
}

func newSecretAPI() (*secretAPI, error) {
	base := strings.TrimRight(os.Getenv("AGMUX_API_URL"), "/")
	if base == "" {
		cfg, err := config.Load()
		if err != nil {
			return nil, fmt.Errorf("load config: %w", err)
		}
		base = fmt.Sprintf("http://localhost:%d", cfg.Server.Port)
	}
	return &secretAPI{baseURL: base, client: &http.Client{Timeout: 60 * time.Second}}, nil
}

func (a *secretAPI) do(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		// Do not wrap the *url.Error: keep the message free of request data.
		return fmt.Errorf("request %s %s failed (is the agmux server running?)", method, path)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp struct {
			Error string `json:"error"`
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err := json.Unmarshal(data, &errResp); err == nil && errResp.Error != "" {
			return errors.New(errResp.Error)
		}
		return fmt.Errorf("API returned status %d", resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func secretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage named secrets and run commands with them injected",
		Long: `Manage named secrets. Values are stored in the macOS Keychain (default) or
in ~/.agmux/secrets and are only ever injected into a child process's
environment by "agmux secret exec". There is intentionally no command that
prints a secret value.`,
		// Errors (e.g. scope denials) are self-explanatory; don't dump usage.
		PersistentPreRun: func(cmd *cobra.Command, args []string) { cmd.SilenceUsage = true },
	}
	cmd.AddCommand(secretAddCmd())
	cmd.AddCommand(secretListCmd())
	cmd.AddCommand(secretRmCmd())
	cmd.AddCommand(secretExecCmd())
	return cmd
}

// readSecretValue reads the value from stdin: without echo when stdin is a
// terminal, otherwise the whole input with one trailing newline removed.
func readSecretValue(name string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprintf(os.Stderr, "Enter value for %s (input hidden): ", name)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read value: %w", err)
		}
		return string(b), nil
	}
	b, err := io.ReadAll(io.LimitReader(os.Stdin, secret.MaxFileValueLength+2))
	if err != nil {
		return "", fmt.Errorf("read value from stdin: %w", err)
	}
	v := string(b)
	if strings.HasSuffix(v, "\r\n") {
		v = strings.TrimSuffix(v, "\r\n")
	} else {
		v = strings.TrimSuffix(v, "\n")
	}
	return v, nil
}

func secretAddCmd() *cobra.Command {
	var description, backend string
	var projects []string
	cmd := &cobra.Command{
		Use:   "add NAME",
		Short: "Register (or replace) a secret; the value is read from stdin",
		Long: `Register a secret named NAME ([A-Z_][A-Z0-9_]*). The value is read from
stdin (hidden input when stdin is a terminal), e.g.

  agmux secret add GITHUB_TOKEN --description "GitHub PAT"
  pbpaste | agmux secret add GITHUB_TOKEN --project ~/src/myrepo

Registering an existing name replaces its value and metadata.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := secret.ValidateName(name); err != nil {
				return err
			}
			if backend != "" {
				if err := secret.ValidateBackendKind(secret.BackendKind(backend)); err != nil {
					return err
				}
			}
			allowed := make([]string, 0, len(projects))
			for _, p := range projects {
				abs, err := filepath.Abs(expandHome(p))
				if err != nil {
					return fmt.Errorf("resolve project %q: %w", p, err)
				}
				allowed = append(allowed, abs)
			}
			api, err := newSecretAPI()
			if err != nil {
				return err
			}
			value, err := readSecretValue(name)
			if err != nil {
				return err
			}
			if value == "" {
				return errors.New("empty value; nothing registered")
			}
			body := map[string]interface{}{
				"name":            name,
				"description":     description,
				"backend":         backend,
				"value":           value,
				"allowedProjects": allowed,
			}
			var sec secret.Secret
			err = api.do(cmd.Context(), http.MethodPost, "/api/secrets", body, &sec)
			value = ""
			body["value"] = ""
			if err != nil {
				return err
			}
			fmt.Printf("Registered secret %s (backend: %s)\n", sec.Name, sec.Backend)
			return nil
		},
	}
	cmd.Flags().StringVarP(&description, "description", "d", "", "Description of the secret")
	cmd.Flags().StringVar(&backend, "backend", "", "Value backend: keychain | file (default: [secret] default_backend in config, else keychain)")
	cmd.Flags().StringArrayVar(&projects, "project", nil, "Restrict use to this project path (repeatable; default: all projects)")
	return cmd
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func secretListCmd() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List secrets (names and metadata only)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := newSecretAPI()
			if err != nil {
				return err
			}
			var list []secret.Secret
			if err := api.do(cmd.Context(), http.MethodGet, "/api/secrets", nil, &list); err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(list)
			}
			if len(list) == 0 {
				fmt.Println("No secrets registered.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tBACKEND\tPROJECTS\tLAST USED\tDESCRIPTION")
			for _, s := range list {
				projects := "(all)"
				if len(s.AllowedProjects) > 0 {
					projects = strings.Join(s.AllowedProjects, ",")
				}
				lastUsed := "-"
				if s.LastUsedAt != nil {
					lastUsed = s.LastUsedAt.Local().Format("2006-01-02 15:04")
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", s.Name, s.Backend, projects, lastUsed, s.Description)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output as JSON")
	return cmd
}

func secretRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm NAME",
		Aliases: []string{"delete", "remove"},
		Short:   "Delete a secret (metadata and stored value)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := newSecretAPI()
			if err != nil {
				return err
			}
			if err := api.do(cmd.Context(), http.MethodDelete, "/api/secrets/"+args[0], nil, nil); err != nil {
				return err
			}
			fmt.Printf("Deleted secret %s\n", args[0])
			return nil
		},
	}
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseEnvBinding parses NAME[=ENV_NAME].
func parseEnvBinding(spec string) (name, envName string, err error) {
	name, envName, hasEnv := strings.Cut(spec, "=")
	if !hasEnv {
		envName = name
	}
	if err := secret.ValidateName(name); err != nil {
		return "", "", err
	}
	if !envNameRe.MatchString(envName) {
		return "", "", fmt.Errorf("invalid environment variable name %q", envName)
	}
	return name, envName, nil
}

func secretExecCmd() *cobra.Command {
	var envSpecs []string
	cmd := &cobra.Command{
		Use:   "exec --env NAME[=ENV_NAME] ... -- COMMAND [ARGS...]",
		Short: "Run a command with secrets injected as environment variables",
		Long: `Run COMMAND with each secret exported as an environment variable
(ENV_NAME defaults to NAME). Any occurrence of an injected value in the
command's stdout/stderr is replaced with ***(NAME). The exit code is
propagated; stdin and signals are forwarded.

The secret's project scope is checked against the current agmux session's
project ($AGMUX_SESSION_ID), or the current directory outside a session.

  agmux secret exec --env GITHUB_TOKEN=GH_TOKEN -- gh api user`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(envSpecs) == 0 {
				return errors.New("at least one --env NAME[=ENV_NAME] is required")
			}
			type binding struct{ name, envName string }
			var bindings []binding
			var names []string
			seen := map[string]bool{}
			for _, spec := range envSpecs {
				n, e, err := parseEnvBinding(spec)
				if err != nil {
					return err
				}
				bindings = append(bindings, binding{n, e})
				if !seen[n] {
					seen[n] = true
					names = append(names, n)
				}
			}

			api, err := newSecretAPI()
			if err != nil {
				return err
			}
			cwd, _ := os.Getwd()
			sessionID := os.Getenv("AGMUX_SESSION_ID")
			var auth struct {
				Project string          `json:"project"`
				Secrets []secret.Secret `json:"secrets"`
			}
			ctx := cmd.Context()
			if err := api.do(ctx, http.MethodPost, "/api/secrets/authorize", map[string]interface{}{
				"names": names, "sessionId": sessionID, "cwd": cwd,
			}, &auth); err != nil {
				return err
			}

			registry, err := secret.DefaultRegistry()
			if err != nil {
				return err
			}
			values := map[string]string{}
			for i := range auth.Secrets {
				s := &auth.Secrets[i]
				backend, err := registry.For(s.Backend)
				if err != nil {
					return fmt.Errorf("secret %s: %w", s.Name, err)
				}
				v, err := backend.Get(ctx, s)
				if errors.Is(err, secret.ErrNotFound) {
					return fmt.Errorf("secret %s: value not found in %s backend (re-register it with `agmux secret add %s`)", s.Name, s.Backend, s.Name)
				}
				if err != nil {
					return fmt.Errorf("secret %s: %w", s.Name, err)
				}
				values[s.Name] = v
			}
			for _, n := range names {
				if _, ok := values[n]; !ok {
					return fmt.Errorf("secret %s was not authorized", n)
				}
			}

			inject := make([]secret.Injection, 0, len(bindings))
			for _, b := range bindings {
				inject = append(inject, secret.Injection{Name: b.name, EnvName: b.envName, Value: values[b.name]})
			}
			code, runErr := secret.Exec(secret.ExecOptions{
				Args:           args,
				BaseEnv:        os.Environ(),
				Inject:         inject,
				Stdin:          os.Stdin,
				Stdout:         os.Stdout,
				Stderr:         os.Stderr,
				ForwardSignals: true,
			})
			values = nil
			inject = nil

			// Best-effort usage report (updates last_used_at).
			usageCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			for _, n := range names {
				_ = api.do(usageCtx, http.MethodPost, "/api/secrets/"+n+"/used", map[string]string{"sessionId": sessionID}, nil)
			}
			cancel()

			if runErr != nil {
				fmt.Fprintf(os.Stderr, "agmux secret exec: %v\n", runErr)
			}
			os.Exit(code)
			return nil
		},
	}
	cmd.Flags().StringArrayVarP(&envSpecs, "env", "e", nil, "Secret to inject as NAME or NAME=ENV_NAME (repeatable)")
	// Everything after the command name belongs to the command.
	cmd.Flags().SetInterspersed(false)
	return cmd
}
