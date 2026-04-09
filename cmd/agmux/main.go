package main

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	agmux "github.com/myuon/agmux"
	"github.com/myuon/agmux/internal/config"
	"github.com/myuon/agmux/internal/daemon"
	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/logging"
	"github.com/myuon/agmux/internal/mcp"

	"github.com/myuon/agmux/internal/server"
	"github.com/myuon/agmux/internal/session"
	"github.com/spf13/cobra"
)

// version is set via -ldflags at build time; defaults to "dev".
var version = "dev"

func main() {
	rootCmd := &cobra.Command{
		Use:   "agmux",
		Short: "AI Agent Multiplexer",
	}

	sesCmd := sessionCmd()
	rootCmd.AddCommand(sesCmd)
	rootCmd.AddCommand(serveCmd())
	rootCmd.AddCommand(mcpCmd())
	rootCmd.AddCommand(logsCmd())
	rootCmd.AddCommand(daemonCmd())
	rootCmd.AddCommand(holderCmd())
	rootCmd.AddCommand(templateCmd())
	rootCmd.AddCommand(updateCmd())

	// Subcommand help template: shows .Use (with args) instead of just .Name
	subCmdHelpTpl := `{{.Short}}

Usage:
  {{.UseLine}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}

Available Commands:{{range .Commands}}{{if .IsAvailableCommand}}
  {{rpad .Use 30}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
	// Apply to all subcommands that have children
	for _, c := range rootCmd.Commands() {
		if c.HasSubCommands() {
			c.SetHelpTemplate(subCmdHelpTpl)
		}
	}

	// Root help template: inlines session subcommands
	rootCmd.SetHelpTemplate(`AI Agent Multiplexer

Usage:
  agmux [command]

Session Commands:
{{range .Commands}}{{if eq .Name "session"}}{{range .Commands}}{{if not .Hidden}}  agmux session {{rpad .Use 30}} {{.Short}}
{{end}}{{end}}{{end}}{{end}}
Other Commands:{{range .Commands}}{{if and (ne .Name "session") (not .Hidden) .IsAvailableCommand}}
  {{rpad .Name .NamePadding}} {{.Short}}{{end}}{{end}}

Flags:
{{.LocalFlags.FlagUsages}}
Template Commands:
  agmux template list                                         # List available templates

Examples:
  agmux session create my-task -m "Fix the login bug" -p ./my-project   # Create a Claude session with default model
  agmux session create codex-task --provider codex --model gpt-5.4 -m "Add tests"  # Create a Codex session with specific model
  agmux session create my-task -t reviewer -m "Review PR" -p ./project  # Create a session using a template
  agmux session list                                          # List all sessions with status, provider, and model
  agmux session send 5LEsz "Please also update the README"    # Send a message to a running session
  agmux session history 5LEsz -n 20                           # Show last 20 conversation entries
  agmux session history 5LEsz --offset 10 -n 20               # Show 20 entries starting from the 11th
  agmux session history 5LEsz -f                              # Follow conversation in realtime
  agmux session info 5LEsz                                    # Show detailed session info (process, socket, stream)
  agmux template list                                         # List available templates from config

Use "agmux [command] --help" for more information about a command.
`)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func initManager(cfg *config.Config, port int, logger *slog.Logger) (session.SessionService, *sql.DB, error) {
	dbPath, err := db.DBPathForPort(port)
	if err != nil {
		return nil, nil, err
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	mgr := session.NewManager(database, cfg.Session.ClaudeCommand, cfg.Claude.ClaudePermissionMode(), cfg.Server.Port, logger, cfg.Session.SystemPrompt)
	mgr.SetCodexCommand(cfg.Session.CodexCommand)
	return mgr, database, nil
}

func serveCmd() *cobra.Command {
	var port int
	var devMode bool
	var frontendDir string

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the agmux server (daemon + web UI)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			// CLI flags override config
			if port == 0 {
				port = cfg.Server.Port
			}

			// Acquire exclusive file lock to prevent multiple server instances
			agmuxDir, err := db.AgmuxDir()
			if err != nil {
				return fmt.Errorf("get agmux dir: %w", err)
			}
			lockPath := filepath.Join(agmuxDir, "server.lock")
			lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
			if err != nil {
				return fmt.Errorf("open lock file: %w", err)
			}
			defer lockFile.Close()
			if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
				return fmt.Errorf("another agmux server is already running (lock: %s)", lockPath)
			}
			defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

			// Daemon logger (slog → stderr + ~/.agmux/agmux.log)
			logFile, logger, err := logging.Setup()
			if err != nil {
				return fmt.Errorf("setup logging: %w", err)
			}
			defer logFile.Close()

			// Set slog default so stream_process etc. use daemon logger
			slog.SetDefault(logger)

			mgr, database, err := initManager(cfg, port, logger)
			if err != nil {
				return err
			}

			hub := server.NewHub(logger)
			go hub.Run()

			var srv *server.Server

			srv = server.New(mgr, hub, devMode, logger, database)

			// Wire managed holder PIDs into external detector so holder
			// processes and their children are not detected as external.
			// SetManagedPIDsFunc must be called BEFORE Start() to avoid
			// the first detect() cycle seeing nil and misclassifying
			// holder-managed processes as external.
			if extDet := srv.ExternalDetector(); extDet != nil {
				extDet.SetManagedPIDsFunc(mgr.ManagedHolderPIDs)
				go extDet.Start()
			}

			// Recover stream processes AFTER server.New() so that
			// SetOnNewLines callback is already registered on the manager.
			mgr.RecoverStreamProcesses()

			// Create controller session (singleton) AFTER recovery so that
			// a surviving controller holder can be reconnected first.
			controllerDir, err := db.ControllerDir()
			if err != nil {
				return fmt.Errorf("controller dir: %w", err)
			}
			controllerSess, err := mgr.CreateController(controllerDir)
			if err != nil {
				logger.Warn("failed to create controller session", "error", err)
			} else {
				logger.Info("controller session ready", "id", controllerSess.ID)
			}

			if !devMode {
				// Resolve frontend directory: CLI flag > config > embedded
				if frontendDir == "" {
					frontendDir = cfg.Server.FrontendDir
				}

				var frontendFS fs.FS
				if frontendDir != "" {
					if info, err := os.Stat(frontendDir); err == nil && info.IsDir() {
						frontendFS = os.DirFS(frontendDir)
						slog.Info("serving frontend from filesystem", "dir", frontendDir)
					} else {
						slog.Warn("frontend-dir not found, falling back to embedded", "dir", frontendDir)
						frontendFS, _ = agmux.FrontendFS()
					}
				} else {
					var err error
					frontendFS, err = agmux.FrontendFS()
					if err != nil {
						return fmt.Errorf("load frontend: %w", err)
					}
				}
				srv.MountFrontend(frontendFS)
			}

			addr := fmt.Sprintf(":%d", port)
			logger.Info(fmt.Sprintf("Starting agmux on http://localhost:%d", port))


			httpSrv := srv.NewHTTPServer(addr)

			// Listen first so we can start serving immediately
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("listen: %w", err)
			}

			// Graceful shutdown on SIGTERM/SIGINT
			shutdownCh := make(chan os.Signal, 1)
			signal.Notify(shutdownCh, syscall.SIGTERM, syscall.SIGINT)

			errCh := make(chan error, 1)
			go func() {
				errCh <- httpSrv.Serve(ln)
			}()

			// Notify after Serve starts, with delay for clients to reconnect
			go func() {
				time.Sleep(3 * time.Second)
				hub.Broadcast(server.Message{
					Type: "server_started",
				})
				logger.Info("Server started, notification sent")
			}()

			select {
			case err := <-errCh:
				return err
			case sig := <-shutdownCh:
				logger.Info(fmt.Sprintf("Received %s, shutting down gracefully...", sig))
				mgr.StopAllStreamProcesses()
				if extDet := srv.ExternalDetector(); extDet != nil {
					extDet.Stop()
				}
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutdownCancel()
				return httpSrv.Shutdown(shutdownCtx)
			}
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 0, "Server port (default: from config or 4321)")
	cmd.Flags().BoolVar(&devMode, "dev", false, "Enable dev mode (CORS for Vite)")
	cmd.Flags().StringVar(&frontendDir, "frontend-dir", "", "Serve frontend from this directory instead of embedded files")

	return cmd
}

func sessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Manage agent sessions",
	}

	cmd.AddCommand(sessionCreateCmd())
	cmd.AddCommand(sessionListCmd())
	cmd.AddCommand(sessionStopCmd())
	cmd.AddCommand(sessionDeleteCmd())
	cmd.AddCommand(sessionForkCmd())
	cmd.AddCommand(sessionSendCmd())
	cmd.AddCommand(sessionBroadcastCmd())
	cmd.AddCommand(sessionHistoryCmd())
	cmd.AddCommand(sessionInfoCmd())

	return cmd
}

func sessionCreateCmd() *cobra.Command {
	var projectPath string
	var prompt string
	var worktree bool
	var provider string
	var model string
	var autoApprove bool
	var parentSessionID string
	var templateName string

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new agent session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// If --template is specified, resolve it first
			systemPrompt := ""
			if templateName != "" {
				tmpl, err := resolveTemplate(templateName)
				if err != nil {
					return err
				}
				// Template values are used as defaults; explicit flags override them
				if !cmd.Flags().Changed("provider") && tmpl.Provider != "" {
					provider = tmpl.Provider
				}
				if !cmd.Flags().Changed("model") && tmpl.Model != "" {
					model = tmpl.Model
				}
				systemPrompt = tmpl.SystemPrompt
			}

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			absPath, err := filepath.Abs(projectPath)
			if err != nil {
				return fmt.Errorf("resolve project path: %w", err)
			}
			mgr, database, err := initManager(cfg, cfg.Server.Port, nil)
			if err != nil {
				return err
			}
			defer database.Close()
			sess, err := mgr.Create(args[0], absPath, prompt, worktree, session.CreateOpts{
				Provider:        session.ProviderName(provider),
				Model:           model,
				FullAuto:        autoApprove,
				SystemPrompt:    systemPrompt,
				ParentSessionID: parentSessionID,
				RoleTemplate:    templateName,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Created session: %s (id: %s)\n", sess.Name, sess.ID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&projectPath, "path", "p", ".", "Project directory path")
	cmd.Flags().StringVarP(&prompt, "message", "m", "", "Initial prompt to send")
	cmd.Flags().BoolVarP(&worktree, "worktree", "w", false, "Create a git worktree for the session")
	cmd.Flags().StringVar(&provider, "provider", "claude", "Provider: claude or codex")
	cmd.Flags().StringVar(&model, "model", "", "Model to use (e.g. claude-sonnet-4-5, o4-mini)")
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", true, "Enable full-auto mode (bypass permission prompts for Codex)")
	cmd.Flags().StringVar(&parentSessionID, "parent", "", "Parent session ID to create a sub-session")
	cmd.Flags().StringVarP(&templateName, "template", "t", "", "Role template name to apply")

	return cmd
}

// resolveTemplate looks up a template by name from config.toml.
func resolveTemplate(name string) (*struct {
	Provider     string
	Model        string
	SystemPrompt string
}, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	for _, t := range cfg.Templates {
		if t.Name == name {
			return &struct {
				Provider     string
				Model        string
				SystemPrompt string
			}{
				Provider:     t.Provider,
				Model:        t.Model,
				SystemPrompt: t.SystemPrompt,
			}, nil
		}
	}
	return nil, fmt.Errorf("template %q not found", name)
}

func sessionForkCmd() *cobra.Command {
	var noContext bool
	cmd := &cobra.Command{
		Use:   "fork <id>",
		Short: "Fork an existing session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			mgr, database, err := initManager(cfg, cfg.Server.Port, nil)
			if err != nil {
				return err
			}
			defer database.Close()
			sess, err := mgr.Fork(args[0], !noContext)
			if err != nil {
				return err
			}
			fmt.Printf("Forked session: %s (id: %s)\n", sess.Name, sess.ID)
			return nil
		},
	}
	cmd.Flags().BoolVar(&noContext, "no-context", false, "Fork without preserving conversation context")
	return cmd
}

func sessionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			mgr, _, err := initManager(cfg, cfg.Server.Port, nil)
			if err != nil {
				return err
			}
			sessions, err := mgr.List()
			if err != nil {
				return err
			}
			if len(sessions) == 0 {
				fmt.Println("No sessions found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tSTATUS\tPROVIDER\tMODEL\tROLE\tPROJECT\tPARENT\tCREATED")
			for _, s := range sessions {
				id := s.ID
				if len(id) > 8 {
					id = id[:8]
				}
				parent := ""
				if s.ParentSessionID != "" {
					parent = s.ParentSessionID
					if len(parent) > 8 {
						parent = parent[:8]
					}
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					id, s.Name, s.Status, s.Provider, s.Model, s.RoleTemplate, s.ProjectPath, parent, s.CreatedAt.Format("2006-01-02 15:04:05"))
			}
			w.Flush()
			return nil
		},
	}
}

func sessionStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop <id>",
		Short: "Stop a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			mgr, _, err := initManager(cfg, cfg.Server.Port, nil)
			if err != nil {
				return err
			}
			id := args[0]
			if err := mgr.Stop(id); err != nil {
				return err
			}
			fmt.Println("Session stopped.")
			return nil
		},
	}
}

func sessionDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			mgr, _, err := initManager(cfg, cfg.Server.Port, nil)
			if err != nil {
				return err
			}
			id := args[0]
			if err := mgr.Delete(id); err != nil {
				return err
			}
			fmt.Println("Session deleted.")
			return nil
		},
	}
}

func sessionSendCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "send <id> <text>",
		Short: "Send text to a session",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			mgr, database, err := initManager(cfg, cfg.Server.Port, nil)
			if err != nil {
				return err
			}
			defer database.Close()
			if err := mgr.SendKeysWithImages(args[0], args[1], nil); err != nil {
				return err
			}
			_ = mgr.UpdateStatus(args[0], session.StatusWorking)
			fmt.Println("Text sent.")
			return nil
		},
	}
}

func sessionBroadcastCmd() *cobra.Command {
	var all bool
	var filter string

	cmd := &cobra.Command{
		Use:   "broadcast <text>",
		Short: "Broadcast a message to multiple sessions",
		Long:  "Send the same message to all active sessions or specify a filter.\nExamples:\n  agmux session broadcast \"Report your progress\"\n  agmux session broadcast --filter all \"Stop work\"",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}
			text := args[0]

			if all {
				filter = "all"
			}
			if filter == "" {
				filter = "active"
			}

			mgr, database, err := initManager(cfg, cfg.Server.Port, nil)
			if err != nil {
				return err
			}
			defer database.Close()

			sessions, err := mgr.List()
			if err != nil {
				return err
			}

			activeStatuses := map[session.Status]bool{
				session.StatusWorking:      true,
				session.StatusIdle:         true,
				session.StatusWaitingInput: true,
			}

			var targetIDs []string
			for _, sess := range sessions {
				if sess.Type == session.TypeExternal {
					continue
				}
				if filter == "all" || activeStatuses[sess.Status] {
					targetIDs = append(targetIDs, sess.ID)
				}
			}

			if len(targetIDs) == 0 {
				fmt.Println("No target sessions found.")
				return nil
			}

			type broadcastResult struct {
				sessionID string
				err       error
			}
			results := make([]broadcastResult, len(targetIDs))
			var wg sync.WaitGroup
			for i, id := range targetIDs {
				wg.Add(1)
				go func(idx int, sessionID string) {
					defer wg.Done()
					err := mgr.SendKeysWithImages(sessionID, text, nil)
					if err == nil {
						_ = mgr.UpdateStatus(sessionID, session.StatusWorking)
					}
					results[idx] = broadcastResult{
						sessionID: sessionID,
						err:       err,
					}
				}(i, id)
			}
			wg.Wait()

			for _, r := range results {
				if r.err != nil {
					fmt.Printf("  %s: error - %s\n", r.sessionID, r.err)
				} else {
					fmt.Printf("  %s: sent\n", r.sessionID)
				}
			}
			fmt.Printf("Broadcast complete: %d session(s)\n", len(results))
			return nil
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "Send to all sessions including stopped/paused (shortcut for --filter all)")
	cmd.Flags().StringVar(&filter, "filter", "", "Filter target sessions: \"active\" (default) or \"all\"")

	return cmd
}

func sessionHistoryCmd() *cobra.Command {
	var lines int
	var offset int
	var follow bool
	var jsonOutput bool

	cmd := &cobra.Command{
		Use:   "history <id>",
		Short: "Show conversation history of a session",
		Long: `Display the conversation history from a session's JSONL stream log.

The session ID can be a short prefix (e.g. first 8 characters from "session list").`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := args[0]

			cfg, _ := config.Load()
			mgr, database, err := initManager(cfg, cfg.Server.Port, nil)
			if err != nil {
				return err
			}
			defer database.Close()

			// Find session by prefix match
			sessions, err := mgr.List()
			if err != nil {
				return err
			}
			var matched *session.Session
			for _, s := range sessions {
				if strings.HasPrefix(s.ID, prefix) {
					if matched != nil {
						return fmt.Errorf("ambiguous session prefix %q: matches %s and %s", prefix, matched.ID, s.ID)
					}
					sCopy := s
					matched = &sCopy
				}
			}
			if matched == nil {
				return fmt.Errorf("no session found matching prefix: %s", prefix)
			}

			// Get stream file path
			streamsDir, err := db.StreamsDir()
			if err != nil {
				return fmt.Errorf("get streams dir: %w", err)
			}
			logPath := filepath.Join(streamsDir, matched.ID+".jsonl")

			if jsonOutput {
				return tailSessionLogRaw(logPath, lines, offset, follow, matched.ClearOffset)
			}
			return tailSessionLogFormatted(logPath, lines, offset, follow, matched.ClearOffset)
		},
	}

	cmd.Flags().IntVarP(&lines, "limit", "n", 50, "Max number of entries to show (0 = all)")
	cmd.Flags().IntVar(&offset, "offset", 0, "Skip first N lines before showing results")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output in realtime")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Output raw JSONL lines")

	return cmd
}

func sessionInfoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "info <id>",
		Short: "Show detailed session information (for debugging)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			prefix := args[0]

			cfg, _ := config.Load()
			mgr, _, err := initManager(cfg, cfg.Server.Port, nil)
			if err != nil {
				return err
			}

			// Find session by prefix match using SessionService
			sessions, err := mgr.List()
			if err != nil {
				return err
			}
			var s *session.Session
			for _, sess := range sessions {
				if strings.HasPrefix(sess.ID, prefix) {
					if s != nil {
						return fmt.Errorf("ambiguous session prefix %q: matches %s and %s", prefix, s.ID, sess.ID)
					}
					sessCopy := sess
					s = &sessCopy
				}
			}
			if s == nil {
				return fmt.Errorf("session not found: %s", prefix)
			}

			// Session metadata
			fmt.Printf("Session:        %s\n", s.ID)
			fmt.Printf("Name:           %s\n", s.Name)
			fmt.Printf("Status:         %s\n", s.Status)
			fmt.Printf("Type:           %s\n", s.Type)
			fmt.Printf("Provider:       %s\n", s.Provider)
			fmt.Printf("Model:          %s\n", s.Model)
			if s.RoleTemplate != "" {
				fmt.Printf("Role:           %s\n", s.RoleTemplate)
			}
			fmt.Printf("Project:        %s\n", s.ProjectPath)
			fmt.Printf("CLI Session ID: %s\n", s.CliSessionID)
			if s.ParentSessionID != "" {
				fmt.Printf("Parent:         %s\n", s.ParentSessionID)
			}
			if s.CurrentTask != "" {
				fmt.Printf("Current Task:   %s\n", s.CurrentTask)
			}
			if s.Goal != "" {
				fmt.Printf("Goal:           %s\n", s.Goal)
			}
			if s.LastError != "" {
				fmt.Printf("Last Error:     %s\n", s.LastError)
			}
			fmt.Printf("Clear Offset:   %d\n", s.ClearOffset)
			fmt.Printf("Created:        %s\n", s.CreatedAt.Format("2006-01-02 15:04:05"))
			fmt.Printf("Updated:        %s\n", s.UpdatedAt.Format("2006-01-02 15:04:05"))

			// Process info
			fmt.Println()
			fmt.Printf("Holder PID:     %d\n", s.HolderPID)
			if s.HolderPID > 0 {
				alive := session.IsHolderAlive(s.HolderPID)
				fmt.Printf("Holder Alive:   %v\n", alive)
				if alive {
					// Find child CLI process
					childPIDs, _ := findChildPIDs(s.HolderPID)
					for _, cpid := range childPIDs {
						fmt.Printf("Child PID:      %d\n", cpid)
					}
				}
			}

			// Socket info
			sockPath := session.SocketPath(s.ID)
			fmt.Printf("Socket Path:    %s\n", sockPath)
			if _, err := os.Stat(sockPath); err == nil {
				fmt.Printf("Socket Exists:  true\n")
			} else {
				fmt.Printf("Socket Exists:  false\n")
			}

			// Stream file info
			streamsDir, _ := db.StreamsDir()
			streamPath := filepath.Join(streamsDir, s.ID+".jsonl")
			fmt.Printf("Stream File:    %s\n", streamPath)
			if fi, err := os.Stat(streamPath); err == nil {
				fmt.Printf("Stream Size:    %s\n", formatBytes(fi.Size()))
			} else {
				fmt.Printf("Stream Size:    (not found)\n")
			}

			return nil
		},
	}
}

func findChildPIDs(parentPID int) ([]int, error) {
	out, err := exec.Command("pgrep", "-P", fmt.Sprintf("%d", parentPID)).Output()
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		var pid int
		if _, err := fmt.Sscanf(line, "%d", &pid); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

func formatBytes(b int64) string {
	switch {
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// readSessionLines reads JSONL lines from a stream file, respecting clearOffset.
func readSessionLines(logPath string, clearOffset int64) ([]string, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return nil, fmt.Errorf("open stream file: %w", err)
	}
	defer f.Close()

	if clearOffset > 0 {
		if _, err := f.Seek(clearOffset, io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek to clear offset: %w", err)
		}
	}

	var allLines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return allLines, nil
}

func tailSessionLogRaw(logPath string, n int, offset int, follow bool, clearOffset int64) error {
	allLines, err := readSessionLines(logPath, clearOffset)
	if err != nil {
		return err
	}

	if offset > 0 && offset < len(allLines) {
		allLines = allLines[offset:]
	} else if offset >= len(allLines) {
		allLines = nil
	}
	if n > 0 && len(allLines) > n {
		allLines = allLines[:n]
	}
	for _, line := range allLines {
		fmt.Println(line)
	}

	if !follow {
		return nil
	}

	// For follow mode, open the file and seek to the end
	f, err := os.Open(logPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sigCh; cancel() }()

	reader := bufio.NewReader(f)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				return err
			}
			fmt.Print(line)
		}
	}
}

func tailSessionLogFormatted(logPath string, n int, offset int, follow bool, clearOffset int64) error {
	allLines, err := readSessionLines(logPath, clearOffset)
	if err != nil {
		return err
	}

	// Filter to displayable lines before applying offset/limit
	var displayable []string
	for _, line := range allLines {
		if isDisplayableLine(line) {
			displayable = append(displayable, line)
		}
	}
	if offset > 0 && offset < len(displayable) {
		displayable = displayable[offset:]
	} else if offset >= len(displayable) {
		displayable = nil
	}
	if n > 0 && len(displayable) > n {
		displayable = displayable[:n]
	}
	for _, line := range displayable {
		formatSessionLine(line)
	}

	if !follow {
		return nil
	}

	f, err := os.Open(logPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sigCh; cancel() }()

	reader := bufio.NewReader(f)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				return err
			}
			formatSessionLine(strings.TrimRight(line, "\n"))
		}
	}
}

func isDisplayableLine(line string) bool {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "//") {
		return false
	}
	var entry struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return false
	}
	switch entry.Type {
	case "stream_event", "rate_limit_event":
		return false
	}
	return true
}

// isTTY reports whether stdout is a terminal.
var isTTY = sync.OnceValue(func() bool {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
})

// color returns the ANSI escape sequence if stdout is a terminal, otherwise empty string.
func color(code string) string {
	if isTTY() {
		return code
	}
	return ""
}

func formatSessionLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "//") {
		return
	}

	var entry struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
		Message struct {
			Role    string `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		Result   string `json:"result"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return
	}

	switch entry.Type {
	case "user":
		// User message: content can be a string
		var text string
		if err := json.Unmarshal(entry.Message.Content, &text); err == nil {
			fmt.Printf("%s[user]%s %s\n", color("\033[1;34m"), color("\033[0m"), text)
			return
		}
		fmt.Printf("%s[user]%s %s\n", color("\033[1;34m"), color("\033[0m"), string(entry.Message.Content))

	case "assistant":
		// Assistant message: content is an array of blocks
		var blocks []struct {
			Type  string `json:"type"`
			Text  string `json:"text"`
			Name  string `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if err := json.Unmarshal(entry.Message.Content, &blocks); err != nil {
			fmt.Printf("%s[assistant]%s %s\n", color("\033[1;32m"), color("\033[0m"), string(entry.Message.Content))
			return
		}
		for _, b := range blocks {
			switch b.Type {
			case "text":
				fmt.Printf("%s[assistant]%s %s\n", color("\033[1;32m"), color("\033[0m"), b.Text)
			case "tool_use":
				inputStr := string(b.Input)
				if len(inputStr) > 200 {
					inputStr = inputStr[:200] + "..."
				}
				fmt.Printf("%s[tool: %s]%s %s\n", color("\033[1;33m"), b.Name, color("\033[0m"), inputStr)
			case "tool_result":
				fmt.Printf("%s[tool_result]%s %s\n", color("\033[0;36m"), color("\033[0m"), b.Text)
			}
		}

	case "result":
		if entry.Result != "" {
			summary := entry.Result
			if len(summary) > 200 {
				summary = summary[:200] + "..."
			}
			fmt.Printf("%s[result:%s]%s %s\n", color("\033[1;35m"), entry.Subtype, color("\033[0m"), summary)
		} else {
			fmt.Printf("%s[result:%s]%s (stop_reason: %s)\n", color("\033[1;35m"), entry.Subtype, color("\033[0m"), entry.StopReason)
		}

	case "system":
		switch entry.Subtype {
		case "init":
			fmt.Printf("%s[system:init]%s session started\n", color("\033[0;90m"), color("\033[0m"))
		case "task_started":
			// skip verbose task events
		case "task_progress":
			// skip
		case "task_notification":
			// skip
		default:
			if entry.Subtype != "" {
				fmt.Printf("%s[system:%s]%s\n", color("\033[0;90m"), entry.Subtype, color("\033[0m"))
			}
		}

	case "stream_event":
		// Skip transient stream events in formatted output

	case "rate_limit_event":
		// Skip rate limit events
	}
}

func mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "mcp",
		Short:  "Run as MCP server (stdio transport)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return mcp.NewServer().Run()
		},
	}
}

func logsCmd() *cobra.Command {
	var follow bool
	var lines int

	cmd := &cobra.Command{
		Use:   "logs <session-id>",
		Short: "Show session stream log",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			streamsDir, err := db.StreamsDir()
			if err != nil {
				return fmt.Errorf("get streams dir: %w", err)
			}
			logPath := filepath.Join(streamsDir, args[0]+".jsonl")

			return tailLogFile(logPath, lines, follow)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output in realtime")
	cmd.Flags().IntVarP(&lines, "lines", "n", 20, "Number of lines to show")

	return cmd
}

func tailLogFile(logPath string, lines int, follow bool) error {
	file, err := os.Open(logPath)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	defer file.Close()

	tailLines, err := readTailLines(file, lines)
	if err != nil {
		return fmt.Errorf("read log file: %w", err)
	}
	for _, line := range tailLines {
		fmt.Println(line)
	}

	if !follow {
		return nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	reader := bufio.NewReader(file)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				return err
			}
			fmt.Print(line)
		}
	}
}

func daemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Manage agmux as a macOS launchd agent",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "install",
		Short: "Install and load launchd agent for agmux serve",
		RunE: func(cmd *cobra.Command, args []string) error {
			return daemon.Install()
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "uninstall",
		Short: "Unload and remove launchd agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			return daemon.Uninstall()
		},
	})

	return cmd
}

func holderCmd() *cobra.Command {
	var sessionID string
	var projectPath string

	cmd := &cobra.Command{
		Use:    "holder",
		Short:  "Run as a holder process for a session (internal use)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if sessionID == "" {
				return fmt.Errorf("--session-id is required")
			}
			if projectPath == "" {
				return fmt.Errorf("--project-path is required")
			}

			// Everything after "--" is the CLI command to execute
			cmdArgs := cmd.Flags().Args()
			if len(cmdArgs) == 0 {
				return fmt.Errorf("no command specified after --")
			}

			// Collect environment from current process
			env := os.Environ()

			return session.RunHolder(sessionID, cmdArgs, projectPath, env)
		},
	}

	cmd.Flags().StringVar(&sessionID, "session-id", "", "Session ID")
	cmd.Flags().StringVar(&projectPath, "project-path", "", "Project directory path")
	// Allow passing arbitrary args after "--"
	cmd.Flags().SetInterspersed(false)

	return cmd
}

// readTailLines reads the last n lines from the file.
func readTailLines(file *os.File, n int) ([]string, error) {
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	var allLines []string
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(allLines) <= n {
		return allLines, nil
	}
	return allLines[len(allLines)-n:], nil
}

func templateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Manage templates",
	}

	cmd.AddCommand(templateListCmd())

	return cmd
}

func templateListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available templates",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("load config: %w", err)
			}

			if len(cfg.Templates) == 0 {
				fmt.Println("No templates configured.")
				fmt.Println("Add templates to ~/.agmux/config.toml:")
				fmt.Println("")
				fmt.Println("  [[templates]]")
				fmt.Println("  name = \"my-template\"")
				fmt.Println("  provider = \"claude\"")
				fmt.Println("  systemPrompt = \"You are a helpful assistant.\"")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tPROVIDER\tMODEL")
			for _, t := range cfg.Templates {
				model := t.Model
				if model == "" {
					model = "(default)"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\n", t.Name, t.Provider, model)
			}
			w.Flush()

			return nil
		},
	}
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func fetchLatestRelease() (*githubRelease, error) {
	req, err := http.NewRequest("GET", "https://api.github.com/repos/myuon/agmux/releases/latest", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release githubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &release, nil
}

func isLaunchdServiceRegistered(label string) bool {
	out, err := exec.Command("launchctl", "list", label).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), label)
}

func restartLaunchdService(label string) error {
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	out, err := exec.Command("launchctl", "kickstart", "-k", domain+"/"+label).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl kickstart failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func updateCmd() *cobra.Command {
	var checkOnly bool

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update agmux to the latest release",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Current version: %s\n", version)
			fmt.Println("Checking for updates...")

			release, err := fetchLatestRelease()
			if err != nil {
				return fmt.Errorf("check for updates: %w", err)
			}

			latest := strings.TrimPrefix(release.TagName, "v")
			current := strings.TrimPrefix(version, "v")

			fmt.Printf("Latest version:  %s\n", release.TagName)

			if current != "dev" && current == latest {
				fmt.Println("already up to date")
				return nil
			}

			if checkOnly {
				if current == "dev" {
					fmt.Println("Running development build; latest release is " + release.TagName)
				} else {
					fmt.Printf("Update available: %s -> %s\n", version, release.TagName)
				}
				return nil
			}

			// Find the right asset for current OS/arch
			assetName := fmt.Sprintf("agmux-%s-%s", runtime.GOOS, runtime.GOARCH)
			var downloadURL string
			for _, asset := range release.Assets {
				if asset.Name == assetName {
					downloadURL = asset.BrowserDownloadURL
					break
				}
			}
			if downloadURL == "" {
				return fmt.Errorf("no binary found for %s/%s in release %s (looked for: %s)", runtime.GOOS, runtime.GOARCH, release.TagName, assetName)
			}

			// Determine path of the running executable
			execPath, err := os.Executable()
			if err != nil {
				return fmt.Errorf("determine executable path: %w", err)
			}
			execPath, err = filepath.EvalSymlinks(execPath)
			if err != nil {
				return fmt.Errorf("resolve executable path: %w", err)
			}

			fmt.Printf("Downloading %s...\n", assetName)

			resp, err := http.Get(downloadURL)
			if err != nil {
				return fmt.Errorf("download binary: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("download returned status %d", resp.StatusCode)
			}

			// Write to a temp file in the same directory as the binary
			dir := filepath.Dir(execPath)
			tmpFile, err := os.CreateTemp(dir, "agmux-update-*")
			if err != nil {
				return fmt.Errorf("create temp file: %w", err)
			}
			tmpPath := tmpFile.Name()
			defer os.Remove(tmpPath) // clean up on failure; no-op if rename succeeded

			if _, err := io.Copy(tmpFile, resp.Body); err != nil {
				tmpFile.Close()
				return fmt.Errorf("write binary: %w", err)
			}
			tmpFile.Close()

			// Make executable
			if err := os.Chmod(tmpPath, 0o755); err != nil {
				return fmt.Errorf("chmod binary: %w", err)
			}

			// Atomically replace the current binary
			if err := os.Rename(tmpPath, execPath); err != nil {
				return fmt.Errorf("replace binary: %w", err)
			}

			fmt.Printf("Updated to %s\n", release.TagName)

			// Restart launchd service if registered
			const serviceLabel = "com.myuon.agmux"
			if isLaunchdServiceRegistered(serviceLabel) {
				fmt.Printf("Restarting launchd service %s...\n", serviceLabel)
				if err := restartLaunchdService(serviceLabel); err != nil {
					fmt.Fprintf(os.Stderr, "warning: failed to restart service: %v\n", err)
				} else {
					fmt.Println("Service restarted.")
				}
			}

			return nil
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false, "Only check for updates, do not download")

	return cmd
}
