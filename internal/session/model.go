package session

import (
	"encoding/json"
	"time"
)

type Status string

const (
	StatusWorking         Status = "working"
	StatusIdle            Status = "idle"
	StatusQuestionWaiting Status = "question_waiting"
	StatusAlignmentNeeded Status = "alignment_needed"
	StatusPaused          Status = "paused"
	StatusStopped         Status = "stopped"
)

type SessionType string

const (
	TypeWorker     SessionType = "worker"
	TypeController SessionType = "controller"
	TypeExternal   SessionType = "external"
)

type OutputMode string

const (
	OutputModeTerminal OutputMode = "terminal"
	OutputModeStream   OutputMode = "stream"
)

type Session struct {
	ID            string       `json:"id"`
	Name          string       `json:"name"`
	ProjectPath   string       `json:"projectPath"`
	InitialPrompt string       `json:"initialPrompt,omitempty"`
	TmuxSession   string       `json:"tmuxSession"`
	Status        Status       `json:"status"`
	Type          SessionType  `json:"type"`
	OutputMode    OutputMode   `json:"outputMode"`
	Provider      ProviderName `json:"provider"`
	CliSessionID  string       `json:"cliSessionId,omitempty"`
	Model         string       `json:"model,omitempty"`
	CurrentTask   string       `json:"currentTask,omitempty"`
	Goal          string       `json:"goal,omitempty"`
	Goals         GoalStack    `json:"goals,omitempty"`
	CreatedAt     time.Time    `json:"createdAt"`
	UpdatedAt     time.Time    `json:"updatedAt"`
}

type GoalEntry struct {
	CurrentTask string    `json:"currentTask"`
	Goal        string    `json:"goal"`
	StartedAt   time.Time `json:"startedAt,omitempty"`
}

type GoalStack []GoalEntry

func (gs GoalStack) Top() *GoalEntry {
	if len(gs) == 0 {
		return nil
	}
	return &gs[len(gs)-1]
}

func (gs GoalStack) Push(entry GoalEntry) GoalStack {
	return append(gs, entry)
}

func (gs GoalStack) Pop() GoalStack {
	if len(gs) == 0 {
		return gs
	}
	return gs[:len(gs)-1]
}

func (gs GoalStack) ToJSON() string {
	if len(gs) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(gs)
	return string(b)
}

func ParseGoalStack(s string) GoalStack {
	if s == "" || s == "[]" {
		return nil
	}
	var gs GoalStack
	if err := json.Unmarshal([]byte(s), &gs); err != nil {
		return nil
	}
	return gs
}
