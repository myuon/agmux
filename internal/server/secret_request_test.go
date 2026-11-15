package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/secret"
	"github.com/myuon/agmux/internal/session"
)

// secretReqStubSessions supports what createEscalation needs.
type secretReqStubSessions struct {
	session.SessionService
}

func (secretReqStubSessions) Get(id string) (*session.Session, error) {
	return &session.Session{ID: id, Name: "sess-" + id, ProjectPath: "/p/" + id}, nil
}
func (secretReqStubSessions) UpdateStatus(string, session.Status) error { return nil }

func newSecretRequestTestEnv(t *testing.T) *secretTestEnv {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	env := newSecretTestServer(t)
	env.s.sessions = secretReqStubSessions{}
	env.s.hub = NewHub(slog.New(slog.NewTextHandler(env.logs, nil)))
	env.s.escalations = NewEscalationStore()
	return env
}

// startSecretEscalation starts a blocking request_secret escalation and waits
// until it is pending. The returned channel yields the escalation response.
func startSecretEscalation(t *testing.T, env *secretTestEnv, sessionID, id, name string) <-chan *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"id": id, "message": "need " + name, "timeout_seconds": 10,
		"secret_name": name, "secret_description": "for tests",
	})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		req := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/escalate", strings.NewReader(string(body)))
		req.Host = "localhost:4321"
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		env.s.Handler().ServeHTTP(rec, req)
		done <- rec
	}()
	deadline := time.Now().Add(5 * time.Second)
	for env.s.escalations.PendingSecretRequest(id, sessionID) == nil {
		select {
		case rec := <-done:
			t.Fatalf("escalate returned early: %d %s", rec.Code, rec.Body)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("secret request did not become pending")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return done
}

func waitEscalation(t *testing.T, done <-chan *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	select {
	case rec := <-done:
		if rec.Code != http.StatusOK {
			t.Fatalf("escalate status = %d body=%s", rec.Code, rec.Body)
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	case <-time.After(5 * time.Second):
		t.Fatal("escalation did not return")
		return nil
	}
}

func drainBroadcasts(h *Hub) string {
	var b strings.Builder
	for {
		select {
		case m := <-h.broadcast:
			data, _ := json.Marshal(m)
			b.Write(data)
		default:
			return b.String()
		}
	}
}

func TestSecretRequest_SaveResolvesEscalation(t *testing.T) {
	env := newSecretRequestTestEnv(t)
	val := testSecretValue()
	done := startSecretEscalation(t, env, "s1", "esc1", "OPENAI_API_KEY")

	// Pending request is visible with the requested name and project.
	rec := doJSON(t, env.s, http.MethodGet, "/api/sessions/s1/escalate", "")
	var pending struct {
		Escalation Escalation `json:"escalation"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &pending)
	if sr := pending.Escalation.SecretRequest; sr == nil || sr.Name != "OPENAI_API_KEY" || sr.ProjectPath != "/p/s1" || sr.Description != "for tests" {
		t.Fatalf("pending = %s", rec.Body)
	}

	// Free-text responses cannot resolve a secret request.
	if rec := doJSON(t, env.s, http.MethodPost, "/api/sessions/s1/escalate/respond", `{"id":"esc1","response":"`+val+`"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("free-text respond status = %d", rec.Code)
	}
	if rec := doJSON(t, env.s, http.MethodPost, "/api/sessions/s1/escalate/respond", `{"id":"esc1","response":"secret_saved"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("fake saved respond status = %d", rec.Code)
	}

	// Wrong session cannot resolve it.
	body, _ := json.Marshal(map[string]any{"id": "esc1", "name": "OTHER", "backend": "file", "value": val})
	if rec := doJSON(t, env.s, http.MethodPost, "/api/sessions/s2/escalate/secret", string(body)); rec.Code != http.StatusNotFound {
		t.Fatalf("wrong session status = %d", rec.Code)
	}
	// Missing value is rejected and the request stays pending.
	if rec := doJSON(t, env.s, http.MethodPost, "/api/sessions/s1/escalate/secret", `{"id":"esc1","backend":"file"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty value status = %d", rec.Code)
	}
	if env.s.escalations.PendingSecretRequest("esc1", "s1") == nil {
		t.Fatal("request resolved by a failed save")
	}

	body, _ = json.Marshal(map[string]any{
		"id": "esc1", "name": "OTHER", "description": "embeddings", "backend": "file", "value": val,
		"allowedProjects": []string{"/p/s1"},
	})
	rec = doJSON(t, env.s, http.MethodPost, "/api/sessions/s1/escalate/secret", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d body=%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), val) {
		t.Fatal("resolve response contains value")
	}
	out := waitEscalation(t, done)
	if out["response"] != SecretResponseSaved || out["timed_out"] != false {
		t.Fatalf("escalation result = %v", out)
	}

	// Stored under the requested name, not the body's.
	if got, _ := env.file.Get(context.Background(), &secret.Secret{Name: "OPENAI_API_KEY"}); got != val {
		t.Fatal("value not stored under the requested name")
	}
	if _, err := env.s.secrets.Get("OTHER"); err == nil {
		t.Fatal("body name was used")
	}
	sec, err := env.s.secrets.Get("OPENAI_API_KEY")
	if err != nil || sec.Description != "embeddings" || len(sec.AllowedProjects) != 1 || sec.AllowedProjects[0] != "/p/s1" {
		t.Fatalf("metadata = %+v err=%v", sec, err)
	}

	// Already resolved.
	if rec := doJSON(t, env.s, http.MethodPost, "/api/sessions/s1/escalate/secret", string(body)); rec.Code != http.StatusNotFound {
		t.Fatalf("second resolve status = %d", rec.Code)
	}

	// The value must not leak to logs, websocket broadcasts, or the DB
	// (notifications are stored there).
	if strings.Contains(env.logs.String(), val) {
		t.Fatal("value found in logs")
	}
	if strings.Contains(drainBroadcasts(env.s.hub), val) {
		t.Fatal("value found in websocket broadcasts")
	}
	for _, p := range []string{env.dbPath, env.dbPath + "-wal"} {
		if data, err := os.ReadFile(p); err == nil && bytes.Contains(data, []byte(val)) {
			t.Fatalf("value found in %s", p)
		}
	}
}

func TestSecretRequest_Cancel(t *testing.T) {
	env := newSecretRequestTestEnv(t)
	done := startSecretEscalation(t, env, "s1", "esc2", "SOME_TOKEN")
	if rec := doJSON(t, env.s, http.MethodPost, "/api/sessions/s1/escalate/respond", `{"id":"esc2","response":"secret_cancelled"}`); rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body=%s", rec.Code, rec.Body)
	}
	out := waitEscalation(t, done)
	if out["response"] != SecretResponseCancelled {
		t.Fatalf("escalation result = %v", out)
	}
	if _, err := env.s.secrets.Get("SOME_TOKEN"); err == nil {
		t.Fatal("secret stored on cancel")
	}
}

func TestSecretRequest_Validation(t *testing.T) {
	env := newSecretRequestTestEnv(t)
	for _, b := range []string{
		`{"id":"e","message":"m","secret_name":"bad-name"}`,
		`{"id":"e","message":"m","secret_name":"OK","login_url":"https://example.com"}`,
	} {
		if rec := doJSON(t, env.s, http.MethodPost, "/api/sessions/s1/escalate", b); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d", b, rec.Code)
		}
	}
	// A plain escalation cannot be resolved through the secret endpoint.
	env.s.escalations.Create("plain", "s1", "q", 10)
	if rec := doJSON(t, env.s, http.MethodPost, "/api/sessions/s1/escalate/secret", `{"id":"plain","value":"x","backend":"file"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("plain escalation status = %d", rec.Code)
	}
}

func TestSecretRequest_OnePasswordRef(t *testing.T) {
	env := newSecretRequestTestEnv(t)
	done := startSecretEscalation(t, env, "s1", "esc-op", "DB_PASSWORD")

	// A value is never accepted for the 1password backend; the request stays pending.
	val := testSecretValue()
	body, _ := json.Marshal(map[string]any{"id": "esc-op", "backend": "1password", "ref": "op://v/i/f", "value": val})
	if rec := doJSON(t, env.s, http.MethodPost, "/api/sessions/s1/escalate/secret", string(body)); rec.Code != http.StatusBadRequest {
		t.Fatalf("value with 1password status = %d", rec.Code)
	}
	if rec := doJSON(t, env.s, http.MethodPost, "/api/sessions/s1/escalate/secret", `{"id":"esc-op","backend":"1password","ref":"nope"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad ref status = %d", rec.Code)
	}
	if env.s.escalations.PendingSecretRequest("esc-op", "s1") == nil {
		t.Fatal("request resolved by a failed save")
	}

	rec := doJSON(t, env.s, http.MethodPost, "/api/sessions/s1/escalate/secret", `{"id":"esc-op","description":"db","backend":"1password","ref":"op://v/i/f"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("resolve status = %d body=%s", rec.Code, rec.Body)
	}
	if out := waitEscalation(t, done); out["response"] != SecretResponseSaved {
		t.Fatalf("escalation result = %v", out)
	}
	sec, err := env.s.secrets.Get("DB_PASSWORD")
	if err != nil || sec.Backend != secret.Backend1Password || sec.Ref != "op://v/i/f" || sec.Description != "db" {
		t.Fatalf("metadata = %+v err=%v", sec, err)
	}
	if strings.Contains(env.logs.String(), val) {
		t.Fatal("value found in logs")
	}
}
