package main

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/myuon/agmux/internal/automation"
	"github.com/spf13/cobra"
)

// Loop task commands (experimental feature "loops"). The daemon answers 404
// while the feature is off.

func (a *automationAPI) listTasks(id string) ([]automation.Task, error) {
	var tasks []automation.Task
	if err := a.do(http.MethodGet, "/api/automations/"+id+"/tasks", nil, &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

func (a *automationAPI) addTask(id, title, body string) (*automation.Task, error) {
	var t automation.Task
	req := map[string]string{"title": title, "body": body}
	if sid := os.Getenv("AGMUX_SESSION_ID"); sid != "" {
		req["sourceSessionId"] = sid
	}
	if err := a.do(http.MethodPost, "/api/automations/"+id+"/tasks", req, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (a *automationAPI) deleteTask(id string, taskID int64) error {
	return a.do(http.MethodDelete, fmt.Sprintf("/api/automations/%s/tasks/%d", id, taskID), nil, nil)
}

func automationTaskCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "task",
		Short: "Manage the remaining-task list of a loop (experimental: loops)",
	}
	cmd.AddCommand(automationTaskAddCmd())
	cmd.AddCommand(automationTaskListCmd())
	cmd.AddCommand(automationTaskRmCmd())
	return cmd
}

func automationTaskAddCmd() *cobra.Command {
	var body string
	cmd := &cobra.Command{
		Use:   "add <automation-id> <title>",
		Short: "Add a task to the remaining-task list",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			api, err := newAutomationAPI()
			if err != nil {
				return err
			}
			id, err := api.resolveAutomationID(args[0])
			if err != nil {
				return err
			}
			t, err := api.addTask(id, args[1], body)
			if err != nil {
				return err
			}
			fmt.Printf("Task added (id: %d).\n", t.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&body, "body", "", "Task details")
	return cmd
}

func automationTaskListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <automation-id>",
		Short: "List tasks of an automation",
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
			tasks, err := api.listTasks(id)
			if err != nil {
				return err
			}
			if len(tasks) == 0 {
				fmt.Println("No tasks found.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tSTATUS\tBY\tATTEMPTS\tTITLE\tRESULT")
			for _, t := range tasks {
				fmt.Fprintf(w, "%d\t%s\t%s\t%d\t%s\t%s\n", t.ID, t.Status, t.CreatedBy, t.Attempts, t.Title, t.Result)
			}
			w.Flush()
			return nil
		},
	}
}

func automationTaskRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <automation-id> <task-id>",
		Short: "Remove a task",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			taskID, err := strconv.ParseInt(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid task id %q", args[1])
			}
			api, err := newAutomationAPI()
			if err != nil {
				return err
			}
			id, err := api.resolveAutomationID(args[0])
			if err != nil {
				return err
			}
			if err := api.deleteTask(id, taskID); err != nil {
				return err
			}
			fmt.Println("Task removed.")
			return nil
		},
	}
}
