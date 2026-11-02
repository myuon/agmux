package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/myuon/agmux/internal/artifact"
	"github.com/myuon/agmux/internal/db"
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
