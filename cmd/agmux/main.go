package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/signal"
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

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func initManager(cfg *config.Config) (*session.Manager, *sql.DB, error) {
	dbPath, err := db.DefaultDBPath()
	if err != nil {
		return nil, nil, err
	}
	database, err := db.Open(dbPath)
	if err != nil {
		return nil, nil, err
	}
	return session.NewManager(database, tmux.NewClient(), cfg.Session.ClaudeCommand, cfg.Server.Port), database, nil
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

			mgr, _, err := initManager(cfg)
			if err != nil {
				return err
			}

			// Logger
			logFile, logger, err := logging.Setup()
			if err != nil {
				return fmt.Errorf("setup logging: %w", err)
			}
			defer logFile.Close()

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
			checker.SetOnNotify(func(sessionName, status, summary string) {
				hub.Broadcast(server.Message{
					Type: "notify",
					Data: map[string]string{
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
			srv := server.New(mgr, hub, devMode, logPath, logger)

			if !devMode {
				frontendFS, err := agmux.FrontendFS()
				if err != nil {
					return fmt.Errorf("load frontend: %w", err)
				}
				srv.MountFrontend(frontendFS)
			}

			addr := fmt.Sprintf(":%d", port)
			logger.Info(fmt.Sprintf("Starting agmux on http://localhost:%d", port))
			logger.Info(fmt.Sprintf("Config: check interval=%s", cfg.Daemon.Interval))

			httpSrv := srv.NewHTTPServer(addr)

			// Graceful shutdown on SIGTERM/SIGINT
			shutdownCh := make(chan os.Signal, 1)
			signal.Notify(shutdownCh, syscall.SIGTERM, syscall.SIGINT)

			errCh := make(chan error, 1)
			go func() {
				errCh <- httpSrv.ListenAndServe()
			}()

			select {
			case err := <-errCh:
				return err
			case sig := <-shutdownCh:
				logger.Info(fmt.Sprintf("Received %s, shutting down gracefully...", sig))
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

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new agent session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			outputMode := session.OutputMode(mode)
			if outputMode != session.OutputModeTerminal && outputMode != session.OutputModeStream {
				return fmt.Errorf("invalid mode %q: must be 'terminal' or 'stream'", mode)
			}
			cfg, _ := config.Load()
			mgr, _, err := initManager(cfg)
			if err != nil {
				return err
			}
			s, err := mgr.Create(args[0], projectPath, prompt, outputMode, worktree)
			if err != nil {
				return err
			}
			fmt.Printf("Created session: %s (id: %s, mode: %s)\n", s.Name, s.ID, s.OutputMode)
			return nil
		},
	}

	cmd.Flags().StringVarP(&projectPath, "path", "p", ".", "Project directory path")
	cmd.Flags().StringVarP(&prompt, "message", "m", "", "Initial prompt to send")
	cmd.Flags().StringVar(&mode, "mode", "terminal", "Output mode: terminal or stream")
	cmd.Flags().BoolVarP(&worktree, "worktree", "w", false, "Create a git worktree for the session")

	return cmd
}

func sessionListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all sessions",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			mgr, _, err := initManager(cfg)
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
			mgr, _, err := initManager(cfg)
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
			mgr, _, err := initManager(cfg)
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
			mgr, _, err := initManager(cfg)
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
			mgr, _, err := initManager(cfg)
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
