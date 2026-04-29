package session

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/myuon/agmux/internal/db"
)

// BackgroundTask mirrors the frontend ActiveTask shape and represents
// a long-running agent / tool task detected from the JSONL stream.
type BackgroundTask struct {
	TaskID            string                  `json:"taskId"`
	TaskType          string                  `json:"taskType"`
	AgentID           string                  `json:"agentId,omitempty"`
	Description       string                  `json:"description,omitempty"`
	StartedAt         string                  `json:"startedAt,omitempty"`
	LastToolName      string                  `json:"lastToolName,omitempty"`
	LastToolInput     interface{}             `json:"lastToolInput,omitempty"`
	Output            string                  `json:"output,omitempty"`
	Usage             *BackgroundTaskUsage    `json:"usage,omitempty"`
	ToolCallHistory   []BackgroundTaskToolCall `json:"toolCallHistory"`
}

// BackgroundTaskUsage tracks token usage reported in task_progress events.
type BackgroundTaskUsage struct {
	InputTokens  *int `json:"inputTokens,omitempty"`
	OutputTokens *int `json:"outputTokens,omitempty"`
}

// BackgroundTaskToolCall is one entry in the tool call history.
type BackgroundTaskToolCall struct {
	ToolName    string `json:"toolName"`
	Description string `json:"description,omitempty"`
	Timestamp   string `json:"timestamp,omitempty"`
}

// BackgroundTaskStore manages background_tasks rows in SQLite.
// Detection mirrors frontend `extractActiveTasks` in
// frontend/src/models/stream.ts.
type BackgroundTaskStore struct {
	db *sql.DB
	mu sync.Mutex
	// pendingToolUseInputs tracks recent tool_use blocks per session by id
	// so that when a task_started event references a tool_use_id we can
	// resolve the tool name / input. Keyed by sessionID, then tool_use_id.
	pendingToolUseInputs map[string]map[string]toolUseRef
}

type toolUseRef struct {
	name  string
	input interface{}
	at    time.Time
}

// NewBackgroundTaskStore creates a store backed by the given SQLite DB.
func NewBackgroundTaskStore(db *sql.DB) *BackgroundTaskStore {
	return &BackgroundTaskStore{
		db:                   db,
		pendingToolUseInputs: make(map[string]map[string]toolUseRef),
	}
}

// rawStreamLine is the minimal shape we need from each JSONL line.
type rawStreamLine struct {
	Type     string                 `json:"type"`
	Subtype  string                 `json:"subtype"`
	TaskID   string                 `json:"task_id"`
	TaskType string                 `json:"task_type"`
	AgentID  string                 `json:"agent_id"`
	ToolUse  string                 `json:"tool_use_id"`
	Desc     string                 `json:"description"`
	Status   string                 `json:"status"`
	Time     string                 `json:"timestamp"`
	LastTool string                 `json:"last_tool_name"`
	LastIn   json.RawMessage        `json:"last_tool_input"`
	Output   string                 `json:"output"`
	Usage    map[string]json.Number `json:"usage"`
	Message  *struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type assistantBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// ApplyLine inspects one JSONL line for the given session and updates the
// background_tasks table accordingly. It is a no-op if the line is not
// task-related. Errors are returned but the caller is expected to log them
// and continue (the JSONL file remains the source of truth for replay).
func (s *BackgroundTaskStore) ApplyLine(sessionID string, line []byte) error {
	if len(line) == 0 || line[0] != '{' {
		return nil
	}
	var ev rawStreamLine
	if err := json.Unmarshal(line, &ev); err != nil {
		return nil
	}

	// 1. Track tool_use inputs from assistant messages so task_started can
	//    resolve `lastToolName` / `lastToolInput` via tool_use_id.
	if ev.Type == "assistant" && ev.Message != nil {
		var blocks []assistantBlock
		if err := json.Unmarshal(ev.Message.Content, &blocks); err == nil {
			for _, b := range blocks {
				if b.Type == "tool_use" && b.ID != "" {
					var input interface{}
					if len(b.Input) > 0 {
						_ = json.Unmarshal(b.Input, &input)
					}
					s.rememberToolUse(sessionID, b.ID, b.Name, input)
					// TaskStop tool calls explicitly remove tasks
					if b.Name == "TaskStop" && len(b.Input) > 0 {
						var stop struct {
							TaskID string `json:"task_id"`
						}
						if err := json.Unmarshal(b.Input, &stop); err == nil && stop.TaskID != "" {
							_ = s.deleteTask(sessionID, stop.TaskID)
						}
					}
				}
			}
		}
		return nil
	}

	if ev.Type != "system" {
		return nil
	}

	switch ev.Subtype {
	case "task_started":
		if ev.TaskID == "" {
			return nil
		}
		taskType := ev.TaskType
		if taskType == "" {
			taskType = "unknown"
		}
		task := BackgroundTask{
			TaskID:          ev.TaskID,
			TaskType:        taskType,
			AgentID:         ev.AgentID,
			Description:     ev.Desc,
			StartedAt:       ev.Time,
			ToolCallHistory: []BackgroundTaskToolCall{},
		}
		if ev.ToolUse != "" {
			if name, input, ok := s.lookupToolUse(sessionID, ev.ToolUse); ok {
				task.LastToolName = name
				task.LastToolInput = input
			}
		}
		return s.upsertTask(sessionID, task)

	case "task_progress":
		if ev.TaskID == "" {
			return nil
		}
		existing, err := s.getTask(sessionID, ev.TaskID)
		if err != nil {
			return err
		}
		if existing == nil {
			taskType := ev.TaskType
			if taskType == "" {
				taskType = "unknown"
			}
			existing = &BackgroundTask{
				TaskID:          ev.TaskID,
				TaskType:        taskType,
				AgentID:         ev.AgentID,
				ToolCallHistory: []BackgroundTaskToolCall{},
			}
		}
		if ev.Desc != "" {
			existing.Description = ev.Desc
		}
		if ev.LastTool != "" {
			last := lastToolCall(existing.ToolCallHistory)
			if last == nil || last.ToolName != ev.LastTool || last.Description != ev.Desc {
				existing.ToolCallHistory = append(existing.ToolCallHistory, BackgroundTaskToolCall{
					ToolName:    ev.LastTool,
					Description: ev.Desc,
					Timestamp:   ev.Time,
				})
			}
			existing.LastToolName = ev.LastTool
		}
		if len(ev.LastIn) > 0 {
			var input interface{}
			if err := json.Unmarshal(ev.LastIn, &input); err == nil {
				existing.LastToolInput = input
			}
		}
		if ev.Output != "" {
			existing.Output = ev.Output
		}
		if len(ev.Usage) > 0 {
			usage := &BackgroundTaskUsage{}
			if v, ok := ev.Usage["input_tokens"]; ok {
				if n, err := v.Int64(); err == nil {
					i := int(n)
					usage.InputTokens = &i
				}
			}
			if v, ok := ev.Usage["output_tokens"]; ok {
				if n, err := v.Int64(); err == nil {
					i := int(n)
					usage.OutputTokens = &i
				}
			}
			existing.Usage = usage
		}
		return s.upsertTask(sessionID, *existing)

	case "task_notification":
		if ev.TaskID == "" {
			return nil
		}
		return s.deleteTask(sessionID, ev.TaskID)
	}

	return nil
}

func lastToolCall(h []BackgroundTaskToolCall) *BackgroundTaskToolCall {
	if len(h) == 0 {
		return nil
	}
	return &h[len(h)-1]
}

// List returns the current background tasks for the given session, ordered
// by started_at ascending (oldest first), to match frontend insertion order.
func (s *BackgroundTaskStore) List(sessionID string) ([]BackgroundTask, error) {
	rows, err := s.db.Query(
		`SELECT task_id, task_type, agent_id, description, started_at,
		        last_tool_name, last_tool_input, output,
		        usage_input_tokens, usage_output_tokens,
		        tool_call_history
		 FROM background_tasks WHERE session_id = ?
		 ORDER BY started_at ASC, task_id ASC`,
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("list background tasks: %w", err)
	}
	defer rows.Close()

	var tasks []BackgroundTask
	for rows.Next() {
		var t BackgroundTask
		var agentID, desc, startedAt, lastToolName, lastToolInput, output sql.NullString
		var inTok, outTok sql.NullInt64
		var history string
		if err := rows.Scan(
			&t.TaskID, &t.TaskType, &agentID, &desc, &startedAt,
			&lastToolName, &lastToolInput, &output,
			&inTok, &outTok,
			&history,
		); err != nil {
			return nil, fmt.Errorf("scan background task: %w", err)
		}
		if agentID.Valid {
			t.AgentID = agentID.String
		}
		if desc.Valid {
			t.Description = desc.String
		}
		if startedAt.Valid {
			t.StartedAt = startedAt.String
		}
		if lastToolName.Valid {
			t.LastToolName = lastToolName.String
		}
		if lastToolInput.Valid && lastToolInput.String != "" {
			var v interface{}
			if err := json.Unmarshal([]byte(lastToolInput.String), &v); err == nil {
				t.LastToolInput = v
			}
		}
		if output.Valid {
			t.Output = output.String
		}
		if inTok.Valid || outTok.Valid {
			u := &BackgroundTaskUsage{}
			if inTok.Valid {
				v := int(inTok.Int64)
				u.InputTokens = &v
			}
			if outTok.Valid {
				v := int(outTok.Int64)
				u.OutputTokens = &v
			}
			t.Usage = u
		}
		if history != "" {
			_ = json.Unmarshal([]byte(history), &t.ToolCallHistory)
		}
		if t.ToolCallHistory == nil {
			t.ToolCallHistory = []BackgroundTaskToolCall{}
		}
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if tasks == nil {
		tasks = []BackgroundTask{}
	}
	return tasks, nil
}

// Dismiss removes a task row.
func (s *BackgroundTaskStore) Dismiss(sessionID, taskID string) error {
	return s.deleteTask(sessionID, taskID)
}

// RebuildFromJSONL clears the existing rows for the session and replays all
// JSONL lines after `clearOffset`. Used after server start / holder reconnect
// so the DB reflects the current state of background tasks even before any
// new live lines arrive.
func (s *BackgroundTaskStore) RebuildFromJSONL(sessionID string, clearOffset int64) error {
	if err := s.ClearSession(sessionID); err != nil {
		return err
	}
	streamsDir, err := db.StreamsDir()
	if err != nil {
		return err
	}
	path := filepath.Join(streamsDir, sessionID+".jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()
	if clearOffset > 0 {
		if _, err := f.Seek(clearOffset, 0); err != nil {
			return err
		}
	}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		_ = s.ApplyLine(sessionID, line)
	}
	return scanner.Err()
}

// ClearSession removes all background task rows for the session, used on
// session clear / restart.
func (s *BackgroundTaskStore) ClearSession(sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM background_tasks WHERE session_id = ?`, sessionID)
	if err != nil {
		return fmt.Errorf("clear background tasks: %w", err)
	}
	s.mu.Lock()
	delete(s.pendingToolUseInputs, sessionID)
	s.mu.Unlock()
	return nil
}

func (s *BackgroundTaskStore) rememberToolUse(sessionID, id, name string, input interface{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.pendingToolUseInputs[sessionID]
	if !ok {
		m = make(map[string]toolUseRef)
		s.pendingToolUseInputs[sessionID] = m
	}
	m[id] = toolUseRef{name: name, input: input, at: time.Now()}
	// Cap memory growth: if we exceed 256 entries, trim the oldest.
	if len(m) > 256 {
		var oldestID string
		var oldestAt time.Time
		for k, v := range m {
			if oldestID == "" || v.at.Before(oldestAt) {
				oldestID = k
				oldestAt = v.at
			}
		}
		delete(m, oldestID)
	}
}

func (s *BackgroundTaskStore) lookupToolUse(sessionID, id string) (string, interface{}, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.pendingToolUseInputs[sessionID]
	if !ok {
		return "", nil, false
	}
	ref, ok := m[id]
	if !ok {
		return "", nil, false
	}
	return ref.name, ref.input, true
}

func (s *BackgroundTaskStore) getTask(sessionID, taskID string) (*BackgroundTask, error) {
	row := s.db.QueryRow(
		`SELECT task_id, task_type, agent_id, description, started_at,
		        last_tool_name, last_tool_input, output,
		        usage_input_tokens, usage_output_tokens,
		        tool_call_history
		 FROM background_tasks WHERE session_id = ? AND task_id = ?`,
		sessionID, taskID,
	)
	var t BackgroundTask
	var agentID, desc, startedAt, lastToolName, lastToolInput, output sql.NullString
	var inTok, outTok sql.NullInt64
	var history string
	err := row.Scan(
		&t.TaskID, &t.TaskType, &agentID, &desc, &startedAt,
		&lastToolName, &lastToolInput, &output,
		&inTok, &outTok,
		&history,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if agentID.Valid {
		t.AgentID = agentID.String
	}
	if desc.Valid {
		t.Description = desc.String
	}
	if startedAt.Valid {
		t.StartedAt = startedAt.String
	}
	if lastToolName.Valid {
		t.LastToolName = lastToolName.String
	}
	if lastToolInput.Valid && lastToolInput.String != "" {
		var v interface{}
		if err := json.Unmarshal([]byte(lastToolInput.String), &v); err == nil {
			t.LastToolInput = v
		}
	}
	if output.Valid {
		t.Output = output.String
	}
	if inTok.Valid || outTok.Valid {
		u := &BackgroundTaskUsage{}
		if inTok.Valid {
			v := int(inTok.Int64)
			u.InputTokens = &v
		}
		if outTok.Valid {
			v := int(outTok.Int64)
			u.OutputTokens = &v
		}
		t.Usage = u
	}
	if history != "" {
		_ = json.Unmarshal([]byte(history), &t.ToolCallHistory)
	}
	if t.ToolCallHistory == nil {
		t.ToolCallHistory = []BackgroundTaskToolCall{}
	}
	return &t, nil
}

func (s *BackgroundTaskStore) upsertTask(sessionID string, t BackgroundTask) error {
	if t.ToolCallHistory == nil {
		t.ToolCallHistory = []BackgroundTaskToolCall{}
	}
	historyJSON, err := json.Marshal(t.ToolCallHistory)
	if err != nil {
		return fmt.Errorf("marshal history: %w", err)
	}
	var lastInputJSON sql.NullString
	if t.LastToolInput != nil {
		b, err := json.Marshal(t.LastToolInput)
		if err == nil {
			lastInputJSON = sql.NullString{String: string(b), Valid: true}
		}
	}
	var inTok, outTok sql.NullInt64
	if t.Usage != nil {
		if t.Usage.InputTokens != nil {
			inTok = sql.NullInt64{Int64: int64(*t.Usage.InputTokens), Valid: true}
		}
		if t.Usage.OutputTokens != nil {
			outTok = sql.NullInt64{Int64: int64(*t.Usage.OutputTokens), Valid: true}
		}
	}
	_, err = s.db.Exec(
		`INSERT INTO background_tasks (
			session_id, task_id, task_type, agent_id, description, started_at,
			last_tool_name, last_tool_input, output,
			usage_input_tokens, usage_output_tokens, tool_call_history,
			updated_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		 ON CONFLICT(session_id, task_id) DO UPDATE SET
			task_type = excluded.task_type,
			agent_id = excluded.agent_id,
			description = excluded.description,
			started_at = COALESCE(NULLIF(excluded.started_at, ''), background_tasks.started_at),
			last_tool_name = excluded.last_tool_name,
			last_tool_input = excluded.last_tool_input,
			output = excluded.output,
			usage_input_tokens = excluded.usage_input_tokens,
			usage_output_tokens = excluded.usage_output_tokens,
			tool_call_history = excluded.tool_call_history,
			updated_at = CURRENT_TIMESTAMP`,
		sessionID, t.TaskID, t.TaskType,
		nullableString(t.AgentID), nullableString(t.Description), nullableString(t.StartedAt),
		nullableString(t.LastToolName), lastInputJSON, nullableString(t.Output),
		inTok, outTok, string(historyJSON),
	)
	if err != nil {
		return fmt.Errorf("upsert background task: %w", err)
	}
	return nil
}

func (s *BackgroundTaskStore) deleteTask(sessionID, taskID string) error {
	_, err := s.db.Exec(
		`DELETE FROM background_tasks WHERE session_id = ? AND task_id = ?`,
		sessionID, taskID,
	)
	if err != nil {
		return fmt.Errorf("delete background task: %w", err)
	}
	return nil
}

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
