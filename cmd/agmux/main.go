package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
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
	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/logging"
	"github.com/myuon/agmux/internal/mcp"
	"github.com/myuon/agmux/internal/monitor"
	"github.com/myuon/agmux/internal/server"
	"github.com/myuon/agmux/internal/session"
	"github.com/myuon/agmux/internal/tmux"
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

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func initManager(cfg *config.Config, logger *slog.Logger) (*session.Manager, *sql.DB, error) {
	dbPath, err := db.DefaultDBPath()
	if err != nil {
		return nil, nil, err
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	mgr := session.NewManager(database, tmux.NewClient(), cfg.Session.ClaudeCommand, cfg.Server.Port, logger, cfg.Session.SystemPrompt)
	mgr.SetCodexCommand(cfg.Session.CodexCommand)
	return mgr, database, nil
}

func serveCmd() *cobra.Command {
	var port int
	var devMode bool

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

			mgr, database, err := initManager(cfg, logger)
			if err != nil {
				return err
			}

			tmuxClient := tmux.NewClient()
			hub := server.NewHub()
			go hub.Run()

			// Status checker
			mon := monitor.New(tmuxClient)
			checker := monitor.NewStatusChecker(mon, mgr, tmuxClient, cfg.Daemon.IntervalDuration(), logger)
			checker.SetOnUpdate(func(sessions []session.Session) {
				hub.Broadcast(server.Message{
					Type: "session_update",
					Data: sessions,
				})
			})
			checker.SetOnNotify(func(sessionId, sessionName, status, summary string) {
				hub.Broadcast(server.Message{
					Type: "notify",
					Data: map[string]string{
						"sessionId":   sessionId,
						"sessionName": sessionName,
						"status":      status,
						"summary":     summary,
					},
				})
			})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go checker.Start(ctx)

			// Recover stream processes for working sessions
			mgr.RecoverStreamProcesses()

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
			srv := server.New(mgr, hub, devMode, logPath, logger, database)

			if !devMode {
				frontendFS, err := agmux.FrontendFS()
				if err != nil {
					return fmt.Errorf("load frontend: %w", err)
				}
				srv.MountFrontend(frontendFS)
			}

			addr := fmt.Sprintf(":%d", port)
			srvLogger.Printf("Starting agmux on http://localhost:%d", port)
			srvLogger.Printf("Config: check interval=%s", cfg.Daemon.Interval)

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
				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer shutdownCancel()
				return httpSrv.Shutdown(shutdownCtx)
			}
		},
	}

	cmd.Flags().IntVarP(&port, "port", "p", 0, "Server port (default: from config or 4321)")
	cmd.Flags().BoolVar(&devMode, "dev", false, "Enable dev mode (CORS for Vite)")

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
	cmd.AddCommand(sessionSendCmd())
	cmd.AddCommand(sessionCaptureCmd())

	return cmd
}

func sessionCreateCmd() *cobra.Command {
	var projectPath string
	var prompt string
	var mode string
	var worktree bool
	var provider string

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new agent session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outputMode := session.OutputMode(mode)
			if outputMode != session.OutputModeTerminal && outputMode != session.OutputModeStream {
				return fmt.Errorf("invalid mode %q: must be 'terminal' or 'stream'", mode)
			}

			if outputMode == session.OutputModeStream {
				// Stream mode: delegate to the running agmux server so the
				// child process outlives this CLI invocation.
				return createSessionViaAPI(args[0], projectPath, prompt, mode, worktree, provider)
			}

			cfg, _ := config.Load()
			mgr, _, err := initManager(cfg, nil)
			if err != nil {
				return err
			}
			s, err := mgr.Create(args[0], projectPath, prompt, outputMode, worktree, session.ProviderName(provider))
			if err != nil {
				return err
			}
			fmt.Printf("Created session: %s (id: %s, mode: %s)\n", s.Name, s.ID, s.OutputMode)
			return nil
		},
	}

	cmd.Flags().StringVarP(&projectPath, "path", "p", ".", "Project directory path")
	cmd.Flags().StringVarP(&prompt, "message", "m", "", "Initial prompt to send")
	cmd.Flags().StringVar(&mode, "mode", "stream", "Output mode: terminal or stream")
	cmd.Flags().BoolVarP(&worktree, "worktree", "w", false, "Create a git worktree for the session")
	cmd.Flags().StringVar(&provider, "provider", "claude", "Provider: claude or codex")

	return cmd
}

// createSessionViaAPI sends a POST /api/sessions request to the running agmux server
// so that the stream process is owned by the server, not this short-lived CLI process.
func createSessionViaAPI(name, projectPath, prompt, mode string, worktree bool, provider string) error {
	cfg, _ := config.Load()
	port := cfg.Server.Port

	absPath, err := filepath.Abs(projectPath)
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}

	body, _ := json.Marshal(map[string]interface{}{
		"name":        name,
		"projectPath": absPath,
		"prompt":      prompt,
		"outputMode":  mode,
		"worktree":    worktree,
		"provider":    provider,
	})

	url := fmt.Sprintf("http://localhost:%d/api/sessions", port)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to connect to agmux server on port %d (is it running?): %w", port, err)
	}
	defer resp.Body.Close()

	var result struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		OutputMode string `json:"outputMode"`
		Error      string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode server response: %w", err)
	}
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("server error: %s", result.Error)
	}

	fmt.Printf("Created session: %s (id: %s, mode: %s)\n", result.Name, result.ID, result.OutputMode)
	return nil
}

func sessionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			mgr, _, err := initManager(cfg, nil)
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
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					s.ID[:8], s.Name, s.Status, s.ProjectPath, s.CreatedAt.Format("2006-01-02 15:04:05"))
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
			mgr, _, err := initManager(cfg, nil)
			if err != nil {
				return err
			}
			id, err := mgr.ResolveID(args[0])
			if err != nil {
				return err
			}
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
			mgr, _, err := initManager(cfg, nil)
			if err != nil {
				return err
			}
			id, err := mgr.ResolveID(args[0])
			if err != nil {
				return err
			}
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
			mgr, _, err := initManager(cfg, nil)
			if err != nil {
				return err
			}
			id, err := mgr.ResolveID(args[0])
			if err != nil {
				return err
			}
			if err := mgr.SendKeys(id, args[1]); err != nil {
				return err
			}
			fmt.Println("Text sent.")
			return nil
		},
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

func sessionCaptureCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "capture <id>",
		Short: "Capture session output",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			mgr, _, err := initManager(cfg, nil)
			if err != nil {
				return err
			}
			id, err := mgr.ResolveID(args[0])
			if err != nil {
				return err
			}
			output, err := mgr.CaptureOutput(id)
			if err != nil {
				return err
			}
			fmt.Print(output)
			return nil
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

// readTailLines reads the last n lines from the file.
func readTailLines(file *os.File, n int) ([]string, error) {
	scanner := bufio.NewScanner(file)
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
