package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/config"
	"github.com/spf13/cobra"
)

// automationAPI is a minimal HTTP client for the daemon's /api/automations
// endpoints. All automation CLI commands go through the HTTP API so that the
// running daemon (scheduler) always sees up-to-date data.
type automationAPI struct {
	baseURL string
	client  *http.Client
}

func newAutomationAPI() (*automationAPI, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return &automationAPI{
		baseURL: fmt.Sprintf("http://localhost:%d", cfg.Server.Port),
		client:  http.DefaultClient,
	}, nil
}

// do sends an HTTP request to the daemon API. body (if non-nil) is encoded as
// JSON; out (if non-nil) receives the decoded JSON response. Non-2xx responses
// are returned as errors using the server's {"error": "..."} payload.
func (a *automationAPI) do(method, path string, body interface{}, out interface{}) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, a.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("request %s %s: %w (is the agmux server running?)", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp struct {
			Error string `json:"error"`
		}
		data, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(data, &errResp); err == nil && errResp.Error != "" {
			return fmt.Errorf("%s", errResp.Error)
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

func (a *automationAPI) list() ([]automation.Automation, error) {
	var automations []automation.Automation
	if err := a.do(http.MethodGet, "/api/automations", nil, &automations); err != nil {
		return nil, err
	}
	return automations, nil
}

// automationCreateRequest mirrors the server's automationRequest body.
type automationCreateRequest struct {
	Name         string `json:"name"`
	Prompt       string `json:"prompt"`
	TriggerType  string `json:"triggerType"`
	TriggerValue string `json:"triggerValue"`
	ProjectPath  string `json:"projectPath,omitempty"`
	Enabled      bool   `json:"enabled"`
}

func (a *automationAPI) create(req automationCreateRequest) (*automation.Automation, error) {
	var created automation.Automation
	if err := a.do(http.MethodPost, "/api/automations", req, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func (a *automationAPI) setEnabled(id string, enabled bool) (*automation.Automation, error) {
	var updated automation.Automation
	body := map[string]bool{"enabled": enabled}
	if err := a.do(http.MethodPut, "/api/automations/"+id+"/enabled", body, &updated); err != nil {
		return nil, err
	}
	return &updated, nil
}

func (a *automationAPI) delete(id string) error {
	return a.do(http.MethodDelete, "/api/automations/"+id, nil, nil)
}

func (a *automationAPI) listRuns(id string, limit int) ([]automation.Run, error) {
	path := "/api/automations/" + id + "/runs"
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	var runs []automation.Run
	if err := a.do(http.MethodGet, path, nil, &runs); err != nil {
		return nil, err
	}
	return runs, nil
}

// resolveAutomationID resolves a (possibly shortened) automation ID prefix to
// the full ID, mirroring the prefix matching used by `session history/info`.
func (a *automationAPI) resolveAutomationID(prefix string) (string, error) {
	automations, err := a.list()
	if err != nil {
		return "", err
	}
	matched := ""
	for _, au := range automations {
		if strings.HasPrefix(au.ID, prefix) {
			if matched != "" {
				return "", fmt.Errorf("ambiguous automation prefix %q: matches %s and %s", prefix, matched, au.ID)
			}
			matched = au.ID
		}
	}
	if matched == "" {
		return "", fmt.Errorf("no automation found matching prefix: %s", prefix)
	}
	return matched, nil
}

func automationCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "automation",
		Short: "Manage automations (scheduled prompts)",
	}

	cmd.AddCommand(automationListCmd())
	cmd.AddCommand(automationCreateCmd())
	cmd.AddCommand(automationEnableCmd())
	cmd.AddCommand(automationDisableCmd())
	cmd.AddCommand(automationDeleteCmd())
	cmd.AddCommand(automationRunsCmd())

	return cmd
}

func formatTrigger(a automation.Automation) string {
	return fmt.Sprintf("%s(%s)", a.TriggerType, a.TriggerValue)
}

func automationListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all automations",
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := newAutomationAPI()
			if err != nil {
				return err
			}
			automations, err := api.list()
			if err != nil {
				return err
			}
			if len(automations) == 0 {
				fmt.Println("No automations found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tNAME\tTRIGGER\tPROJECT\tENABLED")
			for _, a := range automations {
				id := a.ID
				if len(id) > 8 {
					id = id[:8]
				}
				project := a.ProjectPath
				if project == "" {
					project = "(controller)"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%v\n",
					id, a.Name, formatTrigger(a), project, a.Enabled)
			}
			w.Flush()
			return nil
		},
	}
}

func automationCreateCmd() *cobra.Command {
	var name string
	var prompt string
	var interval string
	var cron string
	var projectPath string
	var disabled bool

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new automation",
		Long: `Create a new automation that fires on a schedule and creates a session with the given prompt.

Specify exactly one of --interval (Go duration, e.g. "30m") or --cron (5-field cron expression, e.g. "0 9 * * 1-5").
If --project is omitted, the automation runs in the controller area.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			if prompt == "" {
				return fmt.Errorf("--prompt is required")
			}
			if (interval == "") == (cron == "") {
				return fmt.Errorf("specify exactly one of --interval or --cron")
			}

			triggerType := string(automation.TriggerInterval)
			triggerValue := interval
			if cron != "" {
				triggerType = string(automation.TriggerCron)
				triggerValue = cron
			}

			if projectPath != "" {
				abs, err := filepath.Abs(projectPath)
				if err != nil {
					return fmt.Errorf("resolve project path: %w", err)
				}
				projectPath = abs
			}

			api, err := newAutomationAPI()
			if err != nil {
				return err
			}
			created, err := api.create(automationCreateRequest{
				Name:         name,
				Prompt:       prompt,
				TriggerType:  triggerType,
				TriggerValue: triggerValue,
				ProjectPath:  projectPath,
				Enabled:      !disabled,
			})
			if err != nil {
				return err
			}
			fmt.Printf("Created automation: %s (id: %s)\n", created.Name, created.ID)
			return nil
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "Automation name (required)")
	cmd.Flags().StringVarP(&prompt, "prompt", "m", "", "Prompt sent to the agent when the automation fires (required)")
	cmd.Flags().StringVar(&interval, "interval", "", "Fire at a fixed interval (Go duration, e.g. 30m, 1h)")
	cmd.Flags().StringVar(&cron, "cron", "", "Fire on a cron schedule (5-field expression, e.g. \"0 9 * * 1-5\")")
	cmd.Flags().StringVarP(&projectPath, "project", "p", "", "Project directory path (default: controller area)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "Create the automation in disabled state")

	return cmd
}

func automationEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <id>",
		Short: "Enable an automation",
		Args:  cobra.ExactArgs(1),
		RunE:  setAutomationEnabledRunE(true, "Automation enabled."),
	}
}

func automationDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <id>",
		Short: "Disable an automation",
		Args:  cobra.ExactArgs(1),
		RunE:  setAutomationEnabledRunE(false, "Automation disabled."),
	}
}

func setAutomationEnabledRunE(enabled bool, doneMsg string) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		api, err := newAutomationAPI()
		if err != nil {
			return err
		}
		id, err := api.resolveAutomationID(args[0])
		if err != nil {
			return err
		}
		if _, err := api.setEnabled(id, enabled); err != nil {
			return err
		}
		fmt.Println(doneMsg)
		return nil
	}
}

func automationDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete an automation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := newAutomationAPI()
			if err != nil {
				return err
			}
			id, err := api.resolveAutomationID(args[0])
			if err != nil {
				return err
			}
			if err := api.delete(id); err != nil {
				return err
			}
			fmt.Println("Automation deleted.")
			return nil
		},
	}
}

func automationRunsCmd() *cobra.Command {
	var limit int

	cmd := &cobra.Command{
		Use:   "runs <id>",
		Short: "Show execution history of an automation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := newAutomationAPI()
			if err != nil {
				return err
			}
			id, err := api.resolveAutomationID(args[0])
			if err != nil {
				return err
			}
			runs, err := api.listRuns(id, limit)
			if err != nil {
				return err
			}
			if len(runs) == 0 {
				fmt.Println("No runs found.")
				return nil
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "FIRED_AT\tSTATUS\tSESSION\tMESSAGE")
			for _, r := range runs {
				sessionID := r.SessionID
				if len(sessionID) > 8 {
					sessionID = sessionID[:8]
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
					r.FiredAt.Local().Format("2006-01-02 15:04:05"), r.Status, sessionID, r.Message)
			}
			w.Flush()
			return nil
		},
	}

	cmd.Flags().IntVarP(&limit, "limit", "n", 50, "Max number of runs to show")

	return cmd
}
