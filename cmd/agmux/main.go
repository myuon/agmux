package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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

func main() {
	rootCmd := &cobra.Command{
		Use:   "agmux",
		Short: "AI Agent Multiplexer",
	}

	rootCmd.AddCommand(sessionCmd())
	rootCmd.AddCommand(serveCmd())
	rootCmd.AddCommand(mcpCmd())
	rootCmd.AddCommand(logsCmd())
	rootCmd.AddCommand(daemonCmd())
	rootCmd.AddCommand(holderCmd())

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

			// Server logger (log.Logger → stdout + ~/.agmux/server.log)
			serverLogFile, srvLogger, err := logging.SetupServerLog()
			if err != nil {
				return fmt.Errorf("setup server log: %w", err)
			}
			defer serverLogFile.Close()

			// Set slog default so stream_process etc. use daemon logger
			slog.SetDefault(logger)
			// Redirect standard log (used by chi middleware.Logger) to server log
			log.SetOutput(srvLogger.Writer())
			// Set server logger for WS hub
			server.SetServerLog(srvLogger)

			mgr, database, err := initManager(cfg, port, logger)
			if err != nil {
				return err
			}

			hub := server.NewHub()
			go hub.Run()

			var srv *server.Server

			// Create controller session (singleton)
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

			logPath, _ := logging.LogPath()
			srv = server.New(mgr, hub, devMode, logPath, logger, database)

			// Wire managed holder PIDs into external detector so holder
			// processes and their children are not detected as external.
			// SetManagedPIDsFunc must be called BEFORE Start() to avoid
			// the first detect() cycle seeing nil and misclassifying
			// holder-managed processes as external.
			if extDet := srv.ExternalDetector(); extDet != nil {
				if m, ok := mgr.(*session.Manager); ok {
					extDet.SetManagedPIDsFunc(m.ManagedHolderPIDs)
				}
				go extDet.Start()
			}

			// Recover stream processes AFTER server.New() so that
			// SetOnNewLines callback is already registered on the manager.
			mgr.RecoverStreamProcesses()

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
			srvLogger.Printf("Starting agmux on http://localhost:%d", port)


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
				srvLogger.Println("Server started, notification sent")
			}()

			select {
			case err := <-errCh:
				return err
			case sig := <-shutdownCh:
				srvLogger.Printf("Received %s, shutting down gracefully...", sig)
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

	return cmd
}

func sessionCreateCmd() *cobra.Command {
	var projectPath string
	var prompt string
	var worktree bool
	var provider string
	var model string
	var autoApprove bool

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new agent session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Always delegate to the running agmux server so the
			// child process outlives this CLI invocation.
			return createSessionViaAPI(args[0], projectPath, prompt, worktree, provider, model, autoApprove)
		},
	}

	cmd.Flags().StringVarP(&projectPath, "path", "p", ".", "Project directory path")
	cmd.Flags().StringVarP(&prompt, "message", "m", "", "Initial prompt to send")
	cmd.Flags().BoolVarP(&worktree, "worktree", "w", false, "Create a git worktree for the session")
	cmd.Flags().StringVar(&provider, "provider", "claude", "Provider: claude or codex")
	cmd.Flags().StringVar(&model, "model", "", "Model to use (e.g. claude-sonnet-4-5, o4-mini)")
	cmd.Flags().BoolVar(&autoApprove, "auto-approve", true, "Enable full-auto mode (bypass permission prompts for Codex)")

	return cmd
}

// createSessionViaAPI sends a POST /api/sessions request to the running agmux server
// so that the stream process is owned by the server, not this short-lived CLI process.
func createSessionViaAPI(name, projectPath, prompt string, worktree bool, provider, model string, autoApprove bool) error {
	cfg, _ := config.Load()
	port := cfg.Server.Port

	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}

	payload := map[string]interface{}{
		"name":        name,
		"projectPath": absPath,
		"prompt":      prompt,
		"worktree":    worktree,
		"provider":    provider,
	}
	if model != "" {
		payload["model"] = model
	}
	if autoApprove {
		payload["autoApprove"] = true
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("http://localhost:%d/api/sessions", port)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to connect to agmux server on port %d (is it running?): %w", port, err)
	}
	defer resp.Body.Close()

	var result struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode server response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("server error: %s", result.Error)
	}

	fmt.Printf("Created session: %s (id: %s)\n", result.Name, result.ID)
	return nil
}

func sessionForkCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "fork <id>",
		Short: "Fork an existing session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return forkSessionViaAPI(args[0])
		},
	}
}

func forkSessionViaAPI(id string) error {
	cfg, _ := config.Load()
	port := cfg.Server.Port

	url := fmt.Sprintf("http://localhost:%d/api/sessions/%s/fork", port, id)
	resp, err := http.Post(url, "application/json", nil)
	if err != nil {
		return fmt.Errorf("failed to connect to agmux server on port %d (is it running?): %w", port, err)
	}
	defer resp.Body.Close()

	var result struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode server response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("server error: %s", result.Error)
	}

	fmt.Printf("Forked session: %s (id: %s)\n", result.Name, result.ID)
	return nil
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
			fmt.Fprintln(w, "ID\tNAME\tSTATUS\tPROJECT\tCREATED")
			for _, s := range sessions {
				id := s.ID
				if len(id) > 8 {
					id = id[:8]
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					id, s.Name, s.Status, s.ProjectPath, s.CreatedAt.Format("2006-01-02 15:04:05"))
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
			cfg, _ := config.Load()
			port := cfg.Server.Port
			id := args[0]

			// Always delegate to the server API so the spawned process
			// is owned by the long-lived server.
			return sendSessionViaAPI(id, args[1], port)
		},
	}
}

// sendSessionViaAPI sends a message to a stream session via the server API
// so that the process is owned by the server, not this short-lived CLI process.
func sendSessionViaAPI(id, text string, port int) error {
	body, _ := json.Marshal(map[string]string{
		"text": text,
	})

	url := fmt.Sprintf("http://localhost:%d/api/sessions/%s/send", port, id)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to connect to agmux server on port %d (is it running?): %w", port, err)
	}
	defer resp.Body.Close()

	var result struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode server response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server error: %s", result.Error)
	}

	fmt.Println("Text sent.")
	return nil
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
			cfg, _ := config.Load()
			port := cfg.Server.Port
			text := args[0]

			if all {
				filter = "all"
			}
			if filter == "" {
				filter = "active"
			}

			return broadcastViaAPI(text, nil, filter, port)
		},
	}

	cmd.Flags().BoolVar(&all, "all", false, "Send to all sessions including stopped/paused (shortcut for --filter all)")
	cmd.Flags().StringVar(&filter, "filter", "", "Filter target sessions: \"active\" (default) or \"all\"")

	return cmd
}

func broadcastViaAPI(text string, sessionIDs []string, filter string, port int) error {
	payload := map[string]interface{}{
		"text":   text,
		"filter": filter,
	}
	if len(sessionIDs) > 0 {
		payload["sessionIds"] = sessionIDs
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("http://localhost:%d/api/sessions/broadcast", port)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to connect to agmux server on port %d (is it running?): %w", port, err)
	}
	defer resp.Body.Close()

	var result struct {
		Results []struct {
			SessionID string `json:"sessionId"`
			Status    string `json:"status"`
			Error     string `json:"error,omitempty"`
		} `json:"results"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode server response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server error: %s", result.Error)
	}

	if len(result.Results) == 0 {
		fmt.Println("No target sessions found.")
		return nil
	}

	for _, r := range result.Results {
		if r.Status == "sent" {
			fmt.Printf("  %s: sent\n", r.SessionID)
		} else {
			fmt.Printf("  %s: error - %s\n", r.SessionID, r.Error)
		}
	}
	fmt.Printf("Broadcast complete: %d session(s)\n", len(result.Results))
	return nil
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
	var serverFlag bool
	var daemonFlag bool

	cmd := &cobra.Command{
		Use:   "logs [session-id]",
		Short: "Show logs (session, server, or daemon)",
		Long: `Show logs for a session, the server, or the daemon.

Usage:
  agmux logs <session-id>   Show session stream log
  agmux logs --server       Show server log
  agmux logs --daemon       Show daemon log`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var logPath string

			switch {
			case serverFlag && daemonFlag:
				return fmt.Errorf("cannot specify both --server and --daemon")
			case serverFlag:
				home, err := os.UserHomeDir()
				if err != nil {
					return err
				}
				logPath = filepath.Join(home, ".agmux", "server.log")
			case daemonFlag:
				p, err := logging.LogPath()
				if err != nil {
					return fmt.Errorf("get log path: %w", err)
				}
				logPath = p
			case len(args) == 1:
				streamsDir, err := db.StreamsDir()
				if err != nil {
					return fmt.Errorf("get streams dir: %w", err)
				}
				logPath = filepath.Join(streamsDir, args[0]+".jsonl")
			default:
				return cmd.Help()
			}

			return tailLogFile(logPath, lines, follow)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "Follow log output in realtime")
	cmd.Flags().IntVarP(&lines, "lines", "n", 20, "Number of lines to show")
	cmd.Flags().BoolVar(&serverFlag, "server", false, "Show server log")
	cmd.Flags().BoolVar(&daemonFlag, "daemon", false, "Show daemon log")

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
