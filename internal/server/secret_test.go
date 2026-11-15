package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/secret"
	"github.com/myuon/agmux/internal/session"
)

// memBackend is an in-memory secret.Backend for API tests.
type memBackend struct {
	kind  secret.BackendKind
	items map[string]string
}

func (m *memBackend) Kind() secret.BackendKind { return m.kind }
func (m *memBackend) Set(_ context.Context, name, value string) error {
	m.items[name] = value
	return nil
}
func (m *memBackend) Get(_ context.Context, s *secret.Secret) (string, error) {
	v, ok := m.items[s.Name]
	if !ok {
		return "", secret.ErrNotFound
	}
	return v, nil
}
func (m *memBackend) Delete(_ context.Context, name string) error {
	if _, ok := m.items[name]; !ok {
		return secret.ErrNotFound
	}
	delete(m.items, name)
	return nil
}

type secretTestEnv struct {
	s        *Server
	logs     *bytes.Buffer
	dbPath   string
	keychain *memBackend
	file     *secret.FileBackend
	fileDir  string
}

func newSecretTestServer(t *testing.T) *secretTestEnv {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	sqlDB, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	logs := &bytes.Buffer{}
	kc := &memBackend{kind: secret.BackendKeychain, items: map[string]string{}}
	fileDir := filepath.Join(t.TempDir(), "secrets")
	fb := secret.NewFileBackend(fileDir)
	s := &Server{
		logger:    slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		secrets:   secret.NewStore(sqlDB),
		sqlDB:     sqlDB,
		startTime: time.Now(),
		secretBackends: secret.Registry{
			secret.BackendKeychain:  kc,
			secret.BackendFile:      fb,
			secret.Backend1Password: secret.NewOnePasswordBackend(nil, nil),
		},
	}
	s.setupRoutes()
	return &secretTestEnv{s: s, logs: logs, dbPath: dbPath, keychain: kc, file: fb, fileDir: fileDir}
}

func testSecretValue() string {
	// Built at runtime: never commit token-looking literals.
	return "ghp" + "_" + strings.Repeat("Zq9", 12)
}

func TestSecretAPI_RegisterListDelete(t *testing.T) {
	// Isolate config.Load() (default backend lookup) from the real ~/.agmux.
	t.Setenv("HOME", t.TempDir())
	env := newSecretTestServer(t)
	val := testSecretValue()

	body, _ := json.Marshal(map[string]any{
		"name": "GH_TOKEN", "description": "github", "value": val,
		"allowedProjects": []string{"/p/a"},
	})
	rec := doJSON(t, env.s, http.MethodPost, "/api/secrets", string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d body=%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), val) {
		t.Fatal("register response contains value")
	}
	var created secret.Secret
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Backend != secret.BackendKeychain {
		t.Fatalf("default backend = %q, want keychain", created.Backend)
	}
	if env.keychain.items["GH_TOKEN"] != val {
		t.Fatal("value not stored in backend")
	}

	// Re-register into the file backend: updates metadata, moves the value.
	body, _ = json.Marshal(map[string]any{"name": "GH_TOKEN", "backend": "file", "value": val})
	rec = doJSON(t, env.s, http.MethodPost, "/api/secrets", string(body))
	if rec.Code != http.StatusOK {
		t.Fatalf("re-register status = %d body=%s", rec.Code, rec.Body)
	}
	if _, ok := env.keychain.items["GH_TOKEN"]; ok {
		t.Fatal("stale keychain value not removed after backend change")
	}
	if got, _ := env.file.Get(context.Background(), &secret.Secret{Name: "GH_TOKEN"}); got != val {
		t.Fatal("value not stored in file backend")
	}

	// Mark used.
	rec = doJSON(t, env.s, http.MethodPost, "/api/secrets/GH_TOKEN/used", `{"sessionId":"s1"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("used status = %d", rec.Code)
	}

	rec = doJSON(t, env.s, http.MethodGet, "/api/secrets", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), val) {
		t.Fatal("list response contains value")
	}
	var list []secret.Secret
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list) != 1 || list[0].Name != "GH_TOKEN" || list[0].Backend != secret.BackendFile || list[0].LastUsedAt == nil {
		t.Fatalf("unexpected list %+v", list)
	}

	rec = doJSON(t, env.s, http.MethodGet, "/api/secrets/GH_TOKEN", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), val) {
		t.Fatalf("get status=%d leaked=%v", rec.Code, strings.Contains(rec.Body.String(), val))
	}

	rec = doJSON(t, env.s, http.MethodDelete, "/api/secrets/GH_TOKEN", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(env.fileDir, "GH_TOKEN")); !os.IsNotExist(err) {
		t.Fatal("file value not removed on delete")
	}
	rec = doJSON(t, env.s, http.MethodGet, "/api/secrets/GH_TOKEN", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete status = %d", rec.Code)
	}
	rec = doJSON(t, env.s, http.MethodDelete, "/api/secrets/GH_TOKEN", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing status = %d", rec.Code)
	}

	// The value must not appear in logs or in the DB.
	if strings.Contains(env.logs.String(), val) {
		t.Fatal("value found in server logs")
	}
	for _, p := range []string{env.dbPath, env.dbPath + "-wal"} {
		data, err := os.ReadFile(p)
		if err == nil && bytes.Contains(data, []byte(val)) {
			t.Fatalf("value found in %s", p)
		}
	}
}

func TestSecretAPI_Validation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	env := newSecretTestServer(t)
	val := testSecretValue()
	cases := []struct {
		name string
		body map[string]any
	}{
		{"bad name", map[string]any{"name": "bad-name", "value": val}},
		{"missing value", map[string]any{"name": "OK"}},
		{"unknown backend", map[string]any{"name": "OK", "backend": "plain", "value": val}},
		{"1password without ref", map[string]any{"name": "OK", "backend": "1password"}},
		{"1password bad ref", map[string]any{"name": "OK", "backend": "1password", "ref": "nope"}},
		{"1password with value", map[string]any{"name": "OK", "backend": "1password", "ref": "op://v/i/f", "value": val}},
	}
	for _, c := range cases {
		b, _ := json.Marshal(c.body)
		rec := doJSON(t, env.s, http.MethodPost, "/api/secrets", string(b))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", c.name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), val) {
			t.Errorf("%s: error response contains value", c.name)
		}
	}
	// Malformed JSON containing the value must not be echoed back.
	rec := doJSON(t, env.s, http.MethodPost, "/api/secrets", `{"name":"X","value":"`+val+`"`)
	if rec.Code != http.StatusBadRequest || strings.Contains(rec.Body.String(), val) {
		t.Fatalf("malformed body: status=%d body=%s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, env.s, http.MethodPost, "/api/secrets/NOPE/used", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("used missing status = %d", rec.Code)
	}
	if strings.Contains(env.logs.String(), val) {
		t.Fatal("value found in server logs")
	}
}

func TestSecretAPI_RegisterOnePasswordRef(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	env := newSecretTestServer(t)
	rec := doJSON(t, env.s, http.MethodPost, "/api/secrets", `{"name":"DB_PASSWORD","backend":"1password","ref":"op://v/i/f"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body)
	}
	sec, err := env.s.secrets.Get("DB_PASSWORD")
	if err != nil || sec.Backend != secret.Backend1Password || sec.Ref != "op://v/i/f" {
		t.Fatalf("stored = %+v, %v", sec, err)
	}
}

// TestSecretAPI_NoValueEndpoint guards against adding an endpoint that
// returns secret values.
func TestSecretAPI_NoValueEndpoint(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	env := newSecretTestServer(t)
	val := testSecretValue()
	b, _ := json.Marshal(map[string]any{"name": "LEAK_CHECK", "value": val})
	if rec := doJSON(t, env.s, http.MethodPost, "/api/secrets", string(b)); rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d", rec.Code)
	}
	for _, path := range []string{
		"/api/secrets", "/api/secrets/LEAK_CHECK", "/api/secrets/LEAK_CHECK/value",
		"/api/secrets/LEAK_CHECK/reveal", "/api/secrets?reveal=1",
	} {
		rec := doJSON(t, env.s, http.MethodGet, path, "")
		if strings.Contains(rec.Body.String(), val) {
			t.Fatalf("GET %s returned the value", path)
		}
	}
}

func TestSecretAPI_Authorize(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	env := newSecretTestServer(t)
	val := testSecretValue()
	for _, b := range []map[string]any{
		{"name": "SCOPED", "value": val, "allowedProjects": []string{"/p/a"}},
		{"name": "GLOBAL", "value": val},
	} {
		body, _ := json.Marshal(b)
		if rec := doJSON(t, env.s, http.MethodPost, "/api/secrets", string(body)); rec.Code != http.StatusCreated {
			t.Fatalf("register status = %d", rec.Code)
		}
	}
	rec := doJSON(t, env.s, http.MethodPost, "/api/secrets/authorize", `{"names":["SCOPED","GLOBAL"],"cwd":"/p/a/sub"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorize status = %d body=%s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), val) {
		t.Fatal("authorize response contains value")
	}
	var resp secretAuthorizeResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Project != "/p/a/sub" || len(resp.Secrets) != 2 {
		t.Fatalf("unexpected response %+v", resp)
	}
	if rec := doJSON(t, env.s, http.MethodPost, "/api/secrets/authorize", `{"names":["SCOPED"],"cwd":"/other"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope status = %d", rec.Code)
	}
	if rec := doJSON(t, env.s, http.MethodPost, "/api/secrets/authorize", `{"names":["GLOBAL"],"cwd":"/other"}`); rec.Code != http.StatusOK {
		t.Fatalf("global status = %d", rec.Code)
	}
	if rec := doJSON(t, env.s, http.MethodPost, "/api/secrets/authorize", `{"names":["MISSING"]}`); rec.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d", rec.Code)
	}

	// With a session ID, the session's project is authoritative and cwd is
	// ignored; an unknown session is an error (no fallback to cwd).
	env.s.sessions = secretStubSessions{projects: map[string]string{"s1": "/p/a/.claude/worktrees/w", "s2": "/other"}}
	if rec := doJSON(t, env.s, http.MethodPost, "/api/secrets/authorize", `{"names":["SCOPED"],"sessionId":"s1","cwd":"/other"}`); rec.Code != http.StatusOK {
		t.Fatalf("in-scope session status = %d body=%s", rec.Code, rec.Body)
	}
	if rec := doJSON(t, env.s, http.MethodPost, "/api/secrets/authorize", `{"names":["SCOPED"],"sessionId":"s2","cwd":"/p/a"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("out-of-scope session with in-scope cwd status = %d", rec.Code)
	}
	if rec := doJSON(t, env.s, http.MethodPost, "/api/secrets/authorize", `{"names":["GLOBAL"],"sessionId":"nope","cwd":"/p/a"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown session status = %d, want 404", rec.Code)
	}
}

// secretStubSessions maps session ID -> project path. Other SessionService
// methods are unused by the secret API (nil embedded interface).
type secretStubSessions struct {
	session.SessionService
	projects map[string]string
}

func (m secretStubSessions) Get(id string) (*session.Session, error) {
	p, ok := m.projects[id]
	if !ok {
		return nil, errors.New("session not found")
	}
	return &session.Session{ID: id, ProjectPath: p}, nil
}
