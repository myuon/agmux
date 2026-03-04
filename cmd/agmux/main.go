package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"text/tabwriter"

	agmux "github.com/myuon/agmux"
	"github.com/myuon/agmux/internal/config"
	"github.com/myuon/agmux/internal/daemon"
	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/llm"
	"github.com/myuon/agmux/internal/logging"
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
	return session.NewManager(database, tmux.NewClient(), cfg.Session.ClaudeCommand), database, nil
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

			mgr, database, err := initManager(cfg)
			if err != nil {
				return err
			}

			tmuxClient := tmux.NewClient()
			hub := server.NewHub()
			go hub.Run()

			// Status checker (lightweight, 5s interval)
			mon := monitor.New(tmuxClient)
			checker := monitor.NewStatusChecker(mon, mgr, 5*cfg.Daemon.IntervalDuration()/6)
			checker.SetOnUpdate(func(sessions []session.Session) {
				hub.Broadcast(server.Message{
					Type: "session_update",
					Data: sessions,
				})
			})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go checker.Start(ctx)

			// Logger
			logFile, logger, err := logging.Setup()
			if err != nil {
				return fmt.Errorf("setup logging: %w", err)
			}
			defer logFile.Close()

			// Daemon (LLM-powered via local claude CLI)
			llmClient := llm.New(cfg.LLM.Model)
			d := daemon.New(mgr, tmuxClient, llmClient, database, cfg.Daemon.IntervalDuration(), logger)
			d.SetBroadcast(func(actionType string, detail interface{}) {
				hub.Broadcast(server.Message{
					Type: "action_log",
					Data: detail,
				})
			})
			go d.Start(ctx)

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
			srv := server.New(mgr, database, hub, devMode, logPath)

			if !devMode {
				frontendFS, err := agmux.FrontendFS()
				if err != nil {
					return fmt.Errorf("load frontend: %w", err)
				}
				srv.MountFrontend(frontendFS)
			}

			addr := fmt.Sprintf(":%d", port)
			log.Printf("Starting agmux on http://localhost:%d", port)
			log.Printf("Config: daemon interval=%s, model=%s, auto_approve=%v",
				cfg.Daemon.Interval, cfg.LLM.Model, cfg.Daemon.AutoApprove)
			return srv.ListenAndServe(addr)
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

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a new agent session",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _ := config.Load()
			mgr, _, err := initManager(cfg)
			if err != nil {
				return err
			}
			s, err := mgr.Create(args[0], projectPath, prompt)
			if err != nil {
				return err
			}
			fmt.Printf("Created session: %s (id: %s)\n", s.Name, s.ID)
			return nil
		},
	}

	cmd.Flags().StringVarP(&projectPath, "path", "p", ".", "Project directory path")
	cmd.Flags().StringVarP(&prompt, "message", "m", "", "Initial prompt to send")

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
			if err := mgr.Stop(args[0]); err != nil {
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
			if err := mgr.Delete(args[0]); err != nil {
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
			if err := mgr.SendKeys(args[0], args[1]); err != nil {
				return err
			}
			fmt.Println("Text sent.")
			return nil
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
			output, err := mgr.CaptureOutput(args[0])
			if err != nil {
				return err
			}
			fmt.Print(output)
			return nil
		},
	}
}
