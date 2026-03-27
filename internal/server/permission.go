package server

import (
	"encoding/json"
	"sync"
)

// Permission represents a pending permission prompt from Claude Code CLI.
type Permission struct {
	ID             string          `json:"id"`
	SessionID      string          `json:"sessionId"`
	ToolName       string          `json:"toolName"`
	Input          json.RawMessage `json:"input"`
	Response       string          `json:"response,omitempty"`
	Resolved       bool            `json:"resolved"`
	TimedOut       bool            `json:"timedOut"`
	TimeoutSeconds int             `json:"timeoutSeconds"`
}

// PermissionStore manages pending permission prompts with channels for blocking.
type PermissionStore struct {
	mu          sync.Mutex
	permissions map[string]*Permission
	responseCh  map[string]chan string
}

func NewPermissionStore() *PermissionStore {
	return &PermissionStore{
		permissions: make(map[string]*Permission),
		responseCh:  make(map[string]chan string),
	}
}

// Create adds a new permission prompt and returns a channel that will receive the user response.
func (ps *PermissionStore) Create(id, sessionID, toolName string, input json.RawMessage, timeoutSeconds int) chan string {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ch := make(chan string, 1)
	ps.permissions[id] = &Permission{
		ID:             id,
		SessionID:      sessionID,
		ToolName:       toolName,
		Input:          input,
		TimeoutSeconds: timeoutSeconds,
	}
	ps.responseCh[id] = ch
	return ch
}

// Respond sends a response to a pending permission prompt.
func (ps *PermissionStore) Respond(id, response string) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	perm, ok := ps.permissions[id]
	if !ok {
		return false
	}
	perm.Response = response
	perm.Resolved = true

	ch, ok := ps.responseCh[id]
	if ok {
		ch <- response
		close(ch)
		delete(ps.responseCh, id)
	}
	return true
}

// GetBySession returns the most recent pending permission for a session.
func (ps *PermissionStore) GetBySession(sessionID string) *Permission {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, perm := range ps.permissions {
		if perm.SessionID == sessionID && !perm.Resolved {
			return perm
		}
	}
	return nil
}

// MarkTimedOut marks a permission as timed out with an auto-response.
func (ps *PermissionStore) MarkTimedOut(id, autoResponse string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	perm, ok := ps.permissions[id]
	if !ok {
		return
	}
	perm.Response = autoResponse
	perm.Resolved = true
	perm.TimedOut = true

	if ch, ok := ps.responseCh[id]; ok {
		close(ch)
		delete(ps.responseCh, id)
	}
}

// Cleanup removes resolved permissions to prevent memory leaks.
func (ps *PermissionStore) Cleanup(id string) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	delete(ps.permissions, id)
	if ch, ok := ps.responseCh[id]; ok {
		close(ch)
		delete(ps.responseCh, id)
	}
}
