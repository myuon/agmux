package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/myuon/agmux/internal/artifact"
	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/session"
)

func newArtifactTestServer(t *testing.T) (http.Handler, *artifact.Store, string) {
	t.Helper()
	d := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(d, "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	store := artifact.NewStore(sqlDB, filepath.Join(d, "artifacts"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{artifacts: store, logger: logger, hub: NewHub(logger)}
	r := chi.NewRouter()
	s.mountArtifactRaw(r)
	r.Route("/api", func(r chi.Router) { s.mountArtifacts(r) })

	src := filepath.Join(d, "a.html")
	require.NoError(t, os.WriteFile(src, []byte("<h1>hi</h1>"), 0o644))
	id, err := artifact.Publish(store.Dir(), src)
	require.NoError(t, err)
	_, err = store.Register("s1", id, "T")
	require.NoError(t, err)
	return r, store, id
}

func TestRawArtifactHeaders(t *testing.T) {
	h, _, id := newArtifactTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/artifacts/"+id+"/raw", nil))
	require.Equal(t, 200, w.Code)
	assert.Equal(t, "<h1>hi</h1>", w.Body.String())
	assert.Equal(t, "text/html; charset=utf-8", w.Header().Get("Content-Type"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
	assert.Equal(t, "same-origin", w.Header().Get("Cross-Origin-Resource-Policy"))
	csp := w.Header().Get("Content-Security-Policy")
	assert.True(t, strings.HasPrefix(csp, "sandbox allow-scripts;"), csp)
	assert.Contains(t, csp, "connect-src 'none'")
	assert.Contains(t, csp, "form-action 'none'")
	assert.NotContains(t, csp, "allow-same-origin")
	assert.NotContains(t, csp, "allow-top-navigation")
	assert.NotContains(t, csp, "allow-popups")
}

func TestRawArtifactNotFound(t *testing.T) {
	h, _, _ := newArtifactTestServer(t)
	for _, p := range []string{
		"/artifacts/" + strings.Repeat("0", 32) + "/raw",
		"/artifacts/..%2F..%2Fetc%2Fpasswd/raw",
		"/artifacts/index.html/raw",
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", p, nil))
		assert.Equal(t, 404, w.Code, p)
	}
}

func TestDeleteArtifactRemovesFile(t *testing.T) {
	h, store, id := newArtifactTestServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/artifacts/"+id, nil))
	require.Equal(t, 200, w.Code)
	_, err := os.Stat(filepath.Join(store.Dir(), id))
	assert.True(t, os.IsNotExist(err))

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/artifacts/..%2Fx", nil))
	assert.Equal(t, 404, w.Code)
	_, err = os.Stat(store.Dir())
	assert.NoError(t, err)
}

type stubSessions struct {
	session.SessionService
}

func (stubSessions) Get(id string) (*session.Session, error) {
	return &session.Session{ID: id, Name: "sess " + id}, nil
}

// newUpdateTestServer is newArtifactTestServer plus what the update handler
// needs (session lookup and notification storage). It returns the hub so tests
// can observe broadcasts.
func newUpdateTestServer(t *testing.T) (http.Handler, *artifact.Store, string, *Hub) {
	t.Helper()
	d := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(d, "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	store := artifact.NewStore(sqlDB, filepath.Join(d, "artifacts"))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := NewHub(logger)
	s := &Server{artifacts: store, logger: logger, hub: hub, sqlDB: sqlDB, sessions: stubSessions{}}
	r := chi.NewRouter()
	s.mountArtifactRaw(r)
	r.Route("/api", func(r chi.Router) { s.mountArtifacts(r) })
	src := filepath.Join(d, "a.html")
	require.NoError(t, os.WriteFile(src, []byte("<h1>v1</h1>"), 0o644))
	id, err := artifact.Publish(store.Dir(), src)
	require.NoError(t, err)
	_, err = store.Register("s1", id, "T")
	require.NoError(t, err)
	return r, store, id, hub
}

func drain(hub *Hub) []Message {
	var out []Message
	for {
		select {
		case m := <-hub.broadcast:
			out = append(out, m)
		case <-time.After(20 * time.Millisecond):
			return out
		}
	}
}

func putArtifact(h http.Handler, id, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("PUT", "/api/artifacts/"+id, strings.NewReader(body)))
	return w
}

func TestUpdateArtifactBumpsMetadataAndBroadcasts(t *testing.T) {
	h, store, id, hub := newUpdateTestServer(t)
	before, _ := store.Get(id)
	drain(hub)

	w := putArtifact(h, id, `{"title":"T2","sessionId":"other"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"url":"/artifacts/`+id+`"`)

	after, err := store.Get(id)
	require.NoError(t, err)
	assert.Equal(t, "T2", after.Title)
	assert.Equal(t, "s1", after.SessionID, "ownership unchanged even if another session updates")
	assert.Equal(t, before.CreatedAt, after.CreatedAt)
	assert.NotEqual(t, before.UpdatedAt, after.UpdatedAt)

	var updated, notified bool
	for _, m := range drain(hub) {
		switch m.Type {
		case "artifact_updated":
			a := m.Data.(*artifact.Artifact)
			assert.Equal(t, id, a.ID)
			assert.Equal(t, after.UpdatedAt, a.UpdatedAt)
			updated = true
		case "agent_notification":
			d := m.Data.(map[string]interface{})
			assert.Equal(t, "/artifacts/"+id, d["url"])
			assert.Contains(t, d["message"], "更新")
			notified = true
		}
	}
	assert.True(t, updated, "artifact_updated broadcast")
	assert.True(t, notified, "agent_notification broadcast")

	// Title omitted keeps the title.
	require.Equal(t, 200, putArtifact(h, id, `{}`).Code)
	again, _ := store.Get(id)
	assert.Equal(t, "T2", again.Title)
}

func TestUpdateArtifactRejectsUnknownAndTraversal(t *testing.T) {
	h, _, _, hub := newUpdateTestServer(t)
	drain(hub)
	for _, id := range []string{strings.Repeat("0", 32), "..%2Fx", "index.html"} {
		assert.Equal(t, 404, putArtifact(h, id, `{}`).Code, id)
	}
	assert.Empty(t, drain(hub), "nothing broadcast")
}

func TestRawArtifactAfterUpdateKeepsHeaders(t *testing.T) {
	h, store, id, _ := newUpdateTestServer(t)
	before := httptest.NewRecorder()
	h.ServeHTTP(before, httptest.NewRequest("GET", "/artifacts/"+id+"/raw", nil))

	src := filepath.Join(t.TempDir(), "b.html")
	require.NoError(t, os.WriteFile(src, []byte("<h1>v2</h1>"), 0o644))
	rep, err := artifact.Replace(store.Dir(), id, src)
	require.NoError(t, err)
	rep.Commit()
	require.Equal(t, 200, putArtifact(h, id, `{}`).Code)

	for _, path := range []string{"/artifacts/" + id + "/raw", "/artifacts/" + id + "/raw?v=2026-11-05T00:00:00Z"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		require.Equal(t, 200, w.Code)
		assert.Equal(t, "<h1>v2</h1>", w.Body.String(), "query param ignored")
		assert.Equal(t, "private, no-store", w.Header().Get("Cache-Control"))
		assert.Equal(t, before.Header(), w.Header(), "headers identical to pre-update")
		assert.Contains(t, w.Header().Get("Content-Security-Policy"), "sandbox allow-scripts")
		assert.NotContains(t, w.Header().Get("Content-Security-Policy"), "allow-same-origin")
	}
}

func TestListAllArtifacts(t *testing.T) {
	h, store, id1 := newArtifactTestServer(t)
	src := filepath.Join(t.TempDir(), "b.html")
	require.NoError(t, os.WriteFile(src, []byte("<p>b</p>"), 0o644))
	id2, err := artifact.Publish(store.Dir(), src)
	require.NoError(t, err)
	_, err = store.Register("s2", id2, "T2")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/artifacts", nil))
	require.Equal(t, 200, w.Code)
	var got []artifact.Artifact
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(t, got, 2)
	assert.Equal(t, id2, got[0].ID)
	assert.Equal(t, id1, got[1].ID)

	_, err = store.Touch(id1, "")
	require.NoError(t, err)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/artifacts", nil))
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, id1, got[0].ID)
}
