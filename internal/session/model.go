package session

import "time"

type Status string

const (
	StatusRunning Status = "running"
	StatusWaiting Status = "waiting"
	StatusError   Status = "error"
	StatusDone    Status = "done"
	StatusStopped Status = "stopped"
)

type Session struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	ProjectPath   string    `json:"projectPath"`
	InitialPrompt string    `json:"initialPrompt,omitempty"`
	TmuxSession   string    `json:"tmuxSession"`
	Status        Status    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}
