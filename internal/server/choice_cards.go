package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/session"
)

// FeatureChoiceCards is the experimental feature that gates choice cards and
// the offer_choices MCP tool.
const FeatureChoiceCards = "choice_cards"

// Choice card action types.
const (
	ActionSendMessage = "send_message"
	ActionAPI         = "api"
	ActionDismiss     = "dismiss"
)

// Choice card statuses.
const (
	CardOpen      = "open"
	CardAnswered  = "answered"
	CardDismissed = "dismissed"
)

// ChoiceAction is what happens when a choice is pressed.
//   - send_message: Text (or the label when empty) is sent to the session as a user message.
//   - api: the handler registered under Handler is called.
//   - dismiss: nothing happens; the card is just closed.
type ChoiceAction struct {
	Type    string          `json:"type"`
	Text    string          `json:"text,omitempty"`
	Handler string          `json:"handler,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Choice is one button of a card.
type Choice struct {
	ID     string       `json:"id"`
	Label  string       `json:"label"`
	Action ChoiceAction `json:"action"`
}

// ChoiceCard is a set of choices shown in a session's conversation at
// StreamOffset. It never blocks the agent.
type ChoiceCard struct {
	ID           string   `json:"id"`
	SessionID    string   `json:"sessionId"`
	Prompt       string   `json:"prompt"`
	Choices      []Choice `json:"choices"`
	Source       string   `json:"source"` // agent | rule
	RuleID       string   `json:"ruleId,omitempty"`
	Status       string   `json:"status"`
	Answer       string   `json:"answer,omitempty"`
	StreamOffset int      `json:"streamOffset"`
	CreatedAt    string   `json:"createdAt"`
	AnsweredAt   string   `json:"answeredAt,omitempty"`
}

// ChoiceHandler runs an `api` action. Handlers are registered by name with
// RegisterChoiceHandler (used by suggestion rules, #815).
type ChoiceHandler func(ctx context.Context, card ChoiceCard, choice Choice) error

type choiceState struct {
	mu       sync.Mutex
	handlers map[string]ChoiceHandler
}

// RegisterChoiceHandler registers an internal action callable from `api`
// choices as {"type":"api","handler":name}.
func (s *Server) RegisterChoiceHandler(name string, h ChoiceHandler) {
	s.choice.mu.Lock()
	defer s.choice.mu.Unlock()
	if s.choice.handlers == nil {
		s.choice.handlers = map[string]ChoiceHandler{}
	}
	s.choice.handlers[name] = h
}

func (s *Server) choiceHandler(name string) (ChoiceHandler, bool) {
	s.choice.mu.Lock()
	defer s.choice.mu.Unlock()
	h, ok := s.choice.handlers[name]
	return h, ok
}

func choiceCardsEnabled() bool { return experimental.Enabled(FeatureChoiceCards) }

// ChoiceCardInput is the input of CreateChoiceCard.
type ChoiceCardInput struct {
	SessionID string
	Prompt    string
	Choices   []Choice
	Source    string // agent (default) | rule
	RuleID    string
}

// CreateChoiceCard stores a card at the current end of the session's
// conversation, saves a notification and broadcasts it. It returns
// immediately; nothing waits for an answer.
func (s *Server) CreateChoiceCard(in ChoiceCardInput) (*ChoiceCard, error) {
	if strings.TrimSpace(in.Prompt) == "" {
		return nil, errors.New("prompt is required")
	}
	if len(in.Choices) < 2 || len(in.Choices) > 4 {
		return nil, errors.New("choices must have 2 to 4 items")
	}
	choices := make([]Choice, len(in.Choices))
	for i, c := range in.Choices {
		if strings.TrimSpace(c.Label) == "" {
			return nil, errors.New("each choice needs a label")
		}
		if c.ID == "" {
			c.ID = fmt.Sprintf("c%d", i+1)
		}
		switch c.Action.Type {
		case "":
			c.Action.Type = ActionSendMessage
		case ActionSendMessage, ActionDismiss:
		case ActionAPI:
			if c.Action.Handler == "" {
				return nil, errors.New("api action needs a handler")
			}
		default:
			return nil, fmt.Errorf("unknown action type: %s", c.Action.Type)
		}
		choices[i] = c
	}
	source := in.Source
	if source == "" {
		source = "agent"
	}
	sess, err := s.sessions.Get(in.SessionID)
	if err != nil {
		return nil, err
	}
	offset := 0
	if _, _, total, err := s.sessions.GetStreamLinesBefore(in.SessionID, 1<<30, 1); err == nil {
		offset = total
	}
	choicesJSON, _ := json.Marshal(choices)
	card := &ChoiceCard{
		ID:           uuid.New().String(),
		SessionID:    in.SessionID,
		Prompt:       in.Prompt,
		Choices:      choices,
		Source:       source,
		RuleID:       in.RuleID,
		Status:       CardOpen,
		StreamOffset: offset,
		CreatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
	}
	if _, err := s.sqlDB.Exec(
		`INSERT INTO choice_cards (id, session_id, prompt, choices, source, rule_id, status, stream_offset, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 'open', ?, ?)`,
		card.ID, card.SessionID, card.Prompt, string(choicesJSON), card.Source, card.RuleID, card.StreamOffset, card.CreatedAt,
	); err != nil {
		return nil, err
	}

	if err := s.saveNotification(card.SessionID, "notification", card.Prompt); err != nil {
		slog.Error("failed to save choice card notification", "error", err)
	}
	if s.hub != nil {
		s.hub.Broadcast(Message{Type: "choice_card", Data: map[string]interface{}{"sessionId": card.SessionID, "card": card}})
		s.hub.Broadcast(Message{
			Type: "agent_notification",
			Data: map[string]interface{}{"sessionId": card.SessionID, "sessionName": sess.Name, "message": card.Prompt},
		})
	}
	return card, nil
}

const choiceCardColumns = `id, session_id, prompt, choices, source, rule_id, status, answer, stream_offset, created_at, COALESCE(answered_at, '')`

func scanChoiceCard(sc interface{ Scan(...interface{}) error }) (*ChoiceCard, error) {
	var c ChoiceCard
	var choices string
	if err := sc.Scan(&c.ID, &c.SessionID, &c.Prompt, &choices, &c.Source, &c.RuleID, &c.Status, &c.Answer, &c.StreamOffset, &c.CreatedAt, &c.AnsweredAt); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(choices), &c.Choices); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Server) getChoiceCard(id string) (*ChoiceCard, error) {
	return scanChoiceCard(s.sqlDB.QueryRow(`SELECT `+choiceCardColumns+` FROM choice_cards WHERE id = ?`, id))
}

func (s *Server) queryChoiceCards(where string, args ...interface{}) ([]ChoiceCard, error) {
	rows, err := s.sqlDB.Query(`SELECT `+choiceCardColumns+` FROM choice_cards `+where+` ORDER BY created_at, rowid`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ChoiceCard{}
	for rows.Next() {
		c, err := scanChoiceCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func requireChoiceCards(w http.ResponseWriter) bool {
	if !choiceCardsEnabled() {
		writeError(w, http.StatusNotFound, "choice_cards is disabled")
		return false
	}
	return true
}

// createChoiceCardHTTP is the endpoint behind the offer_choices MCP tool. It
// only accepts label/message pairs (send_message actions).
func (s *Server) createChoiceCardHTTP(w http.ResponseWriter, r *http.Request) {
	if !requireChoiceCards(w) {
		return
	}
	var req struct {
		Prompt  string `json:"prompt"`
		Choices []struct {
			Label   string `json:"label"`
			Message string `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	in := ChoiceCardInput{SessionID: chi.URLParam(r, "id"), Prompt: req.Prompt, Source: "agent"}
	if _, err := s.sessions.Get(in.SessionID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	for _, c := range req.Choices {
		in.Choices = append(in.Choices, Choice{Label: c.Label, Action: ChoiceAction{Type: ActionSendMessage, Text: c.Message}})
	}
	card, err := s.CreateChoiceCard(in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, card)
}

func (s *Server) listChoiceCards(w http.ResponseWriter, r *http.Request) {
	if !requireChoiceCards(w) {
		return
	}
	cards, err := s.queryChoiceCards(`WHERE session_id = ?`, chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"cards": cards})
}

// listOpenChoiceCards returns open cards across sessions (session list badges).
func (s *Server) listOpenChoiceCards(w http.ResponseWriter, r *http.Request) {
	if !requireChoiceCards(w) {
		return
	}
	cards, err := s.queryChoiceCards(`WHERE status = 'open'`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"cards": cards})
}

// sendUserMessage sends text to the session as a user message, the same way
// the send endpoint does.
func (s *Server) sendUserMessage(sessionID, text string) error {
	if err := s.sessions.SendKeysWithImages(sessionID, text, nil); err != nil {
		return err
	}
	_ = s.sessions.UpdateStatus(sessionID, session.StatusWorking)
	s.recordSessionAction(sessionID, "session_send_keys", text)
	return nil
}

// closeChoiceCard moves an open card to status. It reports false when the
// card was not open.
func (s *Server) closeChoiceCard(id, status, answer string) (bool, error) {
	res, err := s.sqlDB.Exec(
		`UPDATE choice_cards SET status = ?, answer = ?, answered_at = ? WHERE id = ? AND status = 'open'`,
		status, answer, time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Server) broadcastChoiceCardUpdated(id string) {
	if s.hub == nil {
		return
	}
	if card, err := s.getChoiceCard(id); err == nil {
		s.hub.Broadcast(Message{Type: "choice_card_updated", Data: map[string]interface{}{"sessionId": card.SessionID, "card": card}})
	}
}

func (s *Server) answerChoiceCard(w http.ResponseWriter, r *http.Request) {
	if !requireChoiceCards(w) {
		return
	}
	id := chi.URLParam(r, "id")
	var req struct {
		ChoiceID string `json:"choiceId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	card, err := s.getChoiceCard(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "choice card not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if card.Status != CardOpen {
		writeError(w, http.StatusConflict, "choice card is already "+card.Status)
		return
	}
	var choice *Choice
	for i := range card.Choices {
		if card.Choices[i].ID == req.ChoiceID {
			choice = &card.Choices[i]
		}
	}
	if choice == nil {
		writeError(w, http.StatusBadRequest, "unknown choiceId")
		return
	}

	// Claim the card first so a double click cannot run the action twice.
	status := CardAnswered
	if choice.Action.Type == ActionDismiss {
		status = CardDismissed
	}
	claimed, err := s.closeChoiceCard(id, status, choice.Label)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !claimed {
		writeError(w, http.StatusConflict, "choice card is no longer open")
		return
	}
	if err := s.runChoiceAction(r.Context(), *card, *choice); err != nil {
		// Reopen so the user can retry.
		_, _ = s.sqlDB.Exec(`UPDATE choice_cards SET status = 'open', answer = '', answered_at = NULL WHERE id = ?`, id)
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.broadcastChoiceCardUpdated(id)
	updated, _ := s.getChoiceCard(id)
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) runChoiceAction(ctx context.Context, card ChoiceCard, choice Choice) error {
	switch choice.Action.Type {
	case ActionSendMessage:
		text := choice.Action.Text
		if strings.TrimSpace(text) == "" {
			text = choice.Label
		}
		return s.sendUserMessage(card.SessionID, text)
	case ActionAPI:
		h, ok := s.choiceHandler(choice.Action.Handler)
		if !ok {
			return fmt.Errorf("no handler registered: %s", choice.Action.Handler)
		}
		return h(ctx, card, choice)
	case ActionDismiss:
		return nil
	}
	return fmt.Errorf("unknown action type: %s", choice.Action.Type)
}

func (s *Server) dismissChoiceCard(w http.ResponseWriter, r *http.Request) {
	if !requireChoiceCards(w) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := s.getChoiceCard(id); err != nil {
		writeError(w, http.StatusNotFound, "choice card not found")
		return
	}
	claimed, err := s.closeChoiceCard(id, CardDismissed, "")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !claimed {
		writeError(w, http.StatusConflict, "choice card is no longer open")
		return
	}
	s.broadcastChoiceCardUpdated(id)
	card, _ := s.getChoiceCard(id)
	writeJSON(w, http.StatusOK, card)
}

func (s *Server) mountChoiceCards(r chi.Router) {
	r.Get("/sessions/{id}/choice-cards", s.listChoiceCards)
	r.Post("/sessions/{id}/choice-cards", s.createChoiceCardHTTP)
	r.Get("/choice-cards", s.listOpenChoiceCards)
	r.Post("/choice-cards/{id}/answer", s.answerChoiceCard)
	r.Post("/choice-cards/{id}/dismiss", s.dismissChoiceCard)
}
