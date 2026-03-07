package server

import (
	"sync"
)

// Escalation represents a pending escalation from an agent to a user.
type Escalation struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
	Response  string `json:"response,omitempty"`
	Resolved  bool   `json:"resolved"`
}

// EscalationStore manages pending escalations with channels for blocking.
type EscalationStore struct {
	mu          sync.Mutex
	escalations map[string]*Escalation      // keyed by escalation ID
	responseCh  map[string]chan string       // keyed by escalation ID
}

func NewEscalationStore() *EscalationStore {
	return &EscalationStore{
		escalations: make(map[string]*Escalation),
		responseCh:  make(map[string]chan string),
	}
}

// Create adds a new escalation and returns a channel that will receive the user response.
func (es *EscalationStore) Create(id, sessionID, message string) chan string {
	es.mu.Lock()
	defer es.mu.Unlock()

	ch := make(chan string, 1)
	es.escalations[id] = &Escalation{
		ID:        id,
		SessionID: sessionID,
		Message:   message,
	}
	es.responseCh[id] = ch
	return ch
}

// Respond sends a response to a pending escalation.
func (es *EscalationStore) Respond(id, response string) bool {
	es.mu.Lock()
	defer es.mu.Unlock()

	esc, ok := es.escalations[id]
	if !ok {
		return false
	}
	esc.Response = response
	esc.Resolved = true

	ch, ok := es.responseCh[id]
	if ok {
		ch <- response
		close(ch)
		delete(es.responseCh, id)
	}
	return true
}

// Get returns an escalation by ID.
func (es *EscalationStore) Get(id string) *Escalation {
	es.mu.Lock()
	defer es.mu.Unlock()
	return es.escalations[id]
}

// GetBySession returns the most recent pending escalation for a session.
func (es *EscalationStore) GetBySession(sessionID string) *Escalation {
	es.mu.Lock()
	defer es.mu.Unlock()
	for _, esc := range es.escalations {
		if esc.SessionID == sessionID && !esc.Resolved {
			return esc
		}
	}
	return nil
}

// Cleanup removes resolved escalations to prevent memory leaks.
func (es *EscalationStore) Cleanup(id string) {
	es.mu.Lock()
	defer es.mu.Unlock()
	delete(es.escalations, id)
	if ch, ok := es.responseCh[id]; ok {
		close(ch)
		delete(es.responseCh, id)
	}
}
