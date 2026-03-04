package session

import "time"

type Status string

const (
	StatusWorking         Status = "working"
	StatusIdle            Status = "idle"
	StatusQuestionWaiting Status = "question_waiting"
	StatusStopped         Status = "stopped"
)

type SessionType string

const (
	TypeWorker     SessionType = "worker"
	TypeController SessionType = "controller"
)

type Session struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	ProjectPath   string      `json:"projectPath"`
	InitialPrompt string      `json:"initialPrompt,omitempty"`
	TmuxSession   string      `json:"tmuxSession"`
	Status        Status      `json:"status"`
	Type          SessionType `json:"type"`
	CreatedAt     time.Time   `json:"createdAt"`
	UpdatedAt     time.Time   `json:"updatedAt"`
}
