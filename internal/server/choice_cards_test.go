package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/session"
)

// choiceSessions is a SessionService stub: only the methods choice cards use.
type choiceSessions struct {
	session.SessionService
	total int
	sent  []string
	err   error
}

func (c *choiceSessions) Get(id string) (*session.Session, error) {
	return &session.Session{ID: id, Name: "s-" + id}, nil
}

func (c *choiceSessions) GetStreamLinesBefore(id string, before, limit int) ([]string, int, int, error) {
	return nil, 0, c.total, nil
}

func (c *choiceSessions) SendKeysWithImages(id, text string, images []session.ImageData) error {
	if c.err != nil {
		return c.err
	}
	c.sent = append(c.sent, id+":"+text)
	return nil
}

func (c *choiceSessions) UpdateStatus(id string, status session.Status) error { return nil }

func newChoiceTestServer(t *testing.T) (*Server, *choiceSessions) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	sqlDB, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	sess := &choiceSessions{total: 7}
	s := &Server{logger: logger, hub: NewHub(logger), sessions: sess, sqlDB: sqlDB, startTime: time.Now()}
	s.setupRoutes()
	return s, sess
}

func decodeCard(t *testing.T, body []byte) ChoiceCard {
	t.Helper()
	var c ChoiceCard
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatalf("decode card: %v (%s)", err, body)
	}
	return c
}

func TestChoiceCardsDisabled(t *testing.T) {
	s, _ := newChoiceTestServer(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/sessions/s1/choice-cards"},
		{http.MethodPost, "/api/sessions/s1/choice-cards"},
		{http.MethodGet, "/api/choice-cards"},
		{http.MethodPost, "/api/choice-cards/x/answer"},
		{http.MethodPost, "/api/choice-cards/x/dismiss"},
	} {
		if rec := doJSON(t, s, c.method, c.path, `{}`); rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 while choice_cards is off", c.method, c.path, rec.Code)
		}
	}
}

func TestChoiceCardSendMessageFlow(t *testing.T) {
	s, sess := newChoiceTestServer(t)
	if err := experimental.Set(FeatureChoiceCards, true); err != nil {
		t.Fatal(err)
	}

	// Invalid choice counts are rejected.
	if rec := doJSON(t, s, http.MethodPost, "/api/sessions/s1/choice-cards", `{"prompt":"q","choices":[{"label":"a"}]}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("1 choice = %d, want 400", rec.Code)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/sessions/s1/choice-cards",
		`{"prompt":"進めますか？","choices":[{"label":"はい","message":"はい、進めて"},{"label":"いいえ"}]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", rec.Code, rec.Body.String())
	}
	card := decodeCard(t, rec.Body.Bytes())
	if card.Status != CardOpen || card.StreamOffset != 7 || len(card.Choices) != 2 || card.Choices[0].ID != "c1" {
		t.Fatalf("unexpected card: %+v", card)
	}

	// The notification was saved.
	var n int
	if err := s.sqlDB.QueryRow(`SELECT COUNT(*) FROM notifications WHERE session_id = 's1'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("notifications = %d, err %v", n, err)
	}

	rec = doJSON(t, s, http.MethodGet, "/api/sessions/s1/choice-cards", "")
	var list struct {
		Cards []ChoiceCard `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Cards) != 1 {
		t.Fatalf("list = %s (%v)", rec.Body.String(), err)
	}

	// Answering sends the choice's message as a user message.
	rec = doJSON(t, s, http.MethodPost, "/api/choice-cards/"+card.ID+"/answer", `{"choiceId":"c1"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("answer = %d: %s", rec.Code, rec.Body.String())
	}
	got := decodeCard(t, rec.Body.Bytes())
	if got.Status != CardAnswered || got.Answer != "はい" {
		t.Fatalf("answered card = %+v", got)
	}
	if len(sess.sent) != 1 || sess.sent[0] != "s1:はい、進めて" {
		t.Fatalf("sent = %v", sess.sent)
	}

	// A second answer conflicts and sends nothing.
	if rec := doJSON(t, s, http.MethodPost, "/api/choice-cards/"+card.ID+"/answer", `{"choiceId":"c2"}`); rec.Code != http.StatusConflict {
		t.Fatalf("second answer = %d, want 409", rec.Code)
	}
	if len(sess.sent) != 1 {
		t.Fatalf("sent twice: %v", sess.sent)
	}
}

func TestChoiceCardLabelIsDefaultMessage(t *testing.T) {
	s, sess := newChoiceTestServer(t)
	if err := experimental.Set(FeatureChoiceCards, true); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, s, http.MethodPost, "/api/sessions/s1/choice-cards", `{"prompt":"q","choices":[{"label":"A案"},{"label":"B案"}]}`)
	card := decodeCard(t, rec.Body.Bytes())
	doJSON(t, s, http.MethodPost, "/api/choice-cards/"+card.ID+"/answer", `{"choiceId":"c2"}`)
	if len(sess.sent) != 1 || sess.sent[0] != "s1:B案" {
		t.Fatalf("sent = %v", sess.sent)
	}
}

func TestChoiceCardSendFailureKeepsCardOpen(t *testing.T) {
	s, sess := newChoiceTestServer(t)
	if err := experimental.Set(FeatureChoiceCards, true); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, s, http.MethodPost, "/api/sessions/s1/choice-cards", `{"prompt":"q","choices":[{"label":"A"},{"label":"B"}]}`)
	card := decodeCard(t, rec.Body.Bytes())
	sess.err = errors.New("boom")
	if rec := doJSON(t, s, http.MethodPost, "/api/choice-cards/"+card.ID+"/answer", `{"choiceId":"c1"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("answer = %d, want 500", rec.Code)
	}
	got, err := s.getChoiceCard(card.ID)
	if err != nil || got.Status != CardOpen {
		t.Fatalf("card = %+v, err %v; want open", got, err)
	}
}

func TestChoiceCardDismiss(t *testing.T) {
	s, sess := newChoiceTestServer(t)
	if err := experimental.Set(FeatureChoiceCards, true); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, s, http.MethodPost, "/api/sessions/s1/choice-cards", `{"prompt":"q","choices":[{"label":"A"},{"label":"B"}]}`)
	card := decodeCard(t, rec.Body.Bytes())
	rec = doJSON(t, s, http.MethodPost, "/api/choice-cards/"+card.ID+"/dismiss", `{}`)
	if rec.Code != http.StatusOK || decodeCard(t, rec.Body.Bytes()).Status != CardDismissed {
		t.Fatalf("dismiss = %d: %s", rec.Code, rec.Body.String())
	}
	if len(sess.sent) != 0 {
		t.Fatalf("dismiss must not send: %v", sess.sent)
	}
	rec = doJSON(t, s, http.MethodGet, "/api/choice-cards", "")
	var open struct {
		Cards []ChoiceCard `json:"cards"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &open)
	if len(open.Cards) != 0 {
		t.Fatalf("open cards = %d, want 0", len(open.Cards))
	}
}

func TestChoiceCardAPIAndDismissActions(t *testing.T) {
	s, sess := newChoiceTestServer(t)
	if err := experimental.Set(FeatureChoiceCards, true); err != nil {
		t.Fatal(err)
	}
	var called []string
	s.RegisterChoiceHandler("test.adopt", func(ctx context.Context, card ChoiceCard, choice Choice) error {
		called = append(called, card.SessionID+"/"+choice.ID)
		return nil
	})

	card, err := s.CreateChoiceCard(ChoiceCardInput{
		SessionID: "s1", Prompt: "採用する？", Source: "rule", RuleID: "r1",
		Choices: []Choice{
			{ID: "adopt", Label: "採用", Action: ChoiceAction{Type: ActionAPI, Handler: "test.adopt"}},
			{ID: "no", Label: "不要", Action: ChoiceAction{Type: ActionDismiss}},
			{ID: "missing", Label: "壊れ", Action: ChoiceAction{Type: ActionAPI, Handler: "nope"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if card.Source != "rule" || card.RuleID != "r1" {
		t.Fatalf("card = %+v", card)
	}

	// Unregistered handler: error, card stays open.
	if rec := doJSON(t, s, http.MethodPost, "/api/choice-cards/"+card.ID+"/answer", `{"choiceId":"missing"}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("missing handler = %d, want 500", rec.Code)
	}
	if got, _ := s.getChoiceCard(card.ID); got.Status != CardOpen {
		t.Fatalf("status = %s, want open", got.Status)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/choice-cards/"+card.ID+"/answer", `{"choiceId":"adopt"}`)
	if rec.Code != http.StatusOK || decodeCard(t, rec.Body.Bytes()).Status != CardAnswered {
		t.Fatalf("api answer = %d: %s", rec.Code, rec.Body.String())
	}
	if len(called) != 1 || called[0] != "s1/adopt" {
		t.Fatalf("handler calls = %v", called)
	}
	if len(sess.sent) != 0 {
		t.Fatalf("api action must not send a message: %v", sess.sent)
	}

	// A dismiss-type choice closes the card as dismissed without sending.
	card2, _ := s.CreateChoiceCard(ChoiceCardInput{SessionID: "s1", Prompt: "q", Choices: []Choice{
		{Label: "a", Action: ChoiceAction{Type: ActionDismiss}}, {Label: "b"},
	}})
	doJSON(t, s, http.MethodPost, "/api/choice-cards/"+card2.ID+"/answer", `{"choiceId":"c1"}`)
	if got, _ := s.getChoiceCard(card2.ID); got.Status != CardDismissed || len(sess.sent) != 0 {
		t.Fatalf("dismiss action: status %s, sent %v", got.Status, sess.sent)
	}
}
