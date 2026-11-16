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

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/myuon/agmux/internal/artifact"
	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/session"
)

// projectSessions returns sessions whose project dir is looked up by id.
type projectSessions struct {
	session.SessionService
	projects map[string]string
}

func (p projectSessions) Get(id string) (*session.Session, error) {
	return &session.Session{ID: id, Name: "sess " + id, ProjectPath: p.projects[id]}, nil
}

type dataEnv struct {
	h       http.Handler
	store   *artifact.Store
	home    string
	project string
	other   string // a directory outside the project
}

// newDataTestServer sets HOME to a temp dir (so ~/.agmux is isolated) and
// gives session "s1" a project dir and session "home" the HOME dir itself.
func newDataTestServer(t *testing.T) *dataEnv {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	agmuxDir := filepath.Join(home, ".agmux")
	require.NoError(t, os.MkdirAll(filepath.Join(agmuxDir, "secrets"), 0o700))
	sqlDB, err := db.Open(filepath.Join(agmuxDir, "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	store := artifact.NewStore(sqlDB, filepath.Join(agmuxDir, "artifacts"))
	project := t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &Server{artifacts: store, logger: logger, hub: NewHub(logger), sqlDB: sqlDB,
		sessions: projectSessions{projects: map[string]string{"s1": project, "home": home, "none": ""}}}
	r := chi.NewRouter()
	s.mountArtifactRaw(r)
	r.Route("/api", func(r chi.Router) { s.mountArtifacts(r) })
	return &dataEnv{h: r, store: store, home: home, project: project, other: t.TempDir()}
}

func (e *dataEnv) write(t *testing.T, path, content string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

// publish copies an HTML file and registers it for sessionID with dataFiles.
func (e *dataEnv) publish(t *testing.T, sessionID string, dataFiles map[string]string) (string, *httptest.ResponseRecorder) {
	t.Helper()
	src := e.write(t, filepath.Join(e.other, "page.html"), "<p>page</p>")
	id, err := artifact.Publish(e.store.Dir(), src)
	require.NoError(t, err)
	body, _ := json.Marshal(map[string]interface{}{"id": id, "title": "T", "dataFiles": dataFiles})
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, httptest.NewRequest("POST", "/api/sessions/"+sessionID+"/artifacts", strings.NewReader(string(body))))
	return id, w
}

func (e *dataEnv) get(method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	return w
}

func TestArtifactDataServesFreshContent(t *testing.T) {
	e := newDataTestServer(t)
	f := e.write(t, filepath.Join(e.project, "ideas.json"), `{"v":1}`)
	id, w := e.publish(t, "s1", map[string]string{"ideas.json": f})
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"dataFiles":["ideas.json"]`)

	url := "/artifacts/" + id + "/data/ideas.json"
	r := e.get("GET", url, map[string]string{"Origin": "null"})
	require.Equal(t, 200, r.Code)
	assert.Equal(t, `{"v":1}`, r.Body.String())
	assert.Equal(t, "application/json; charset=utf-8", r.Header().Get("Content-Type"))
	assert.Equal(t, "no-store", r.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", r.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "null", r.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, r.Header().Get("Access-Control-Allow-Credentials"))

	// Edited on disk → next GET returns the new content.
	e.write(t, f, `{"v":2}`)
	r = e.get("GET", url, nil)
	require.Equal(t, 200, r.Code)
	assert.Equal(t, `{"v":2}`, r.Body.String())
	assert.Empty(t, r.Header().Get("Access-Control-Allow-Origin"), "no CORS header for other origins")

	r = e.get("HEAD", url, nil)
	assert.Equal(t, 200, r.Code)
	assert.Empty(t, r.Body.String())

	for _, m := range []string{"POST", "PUT", "DELETE", "OPTIONS"} {
		r = e.get(m, url, nil)
		assert.Equal(t, 405, r.Code, m)
		assert.Equal(t, "GET, HEAD", r.Header().Get("Allow"))
	}
}

func TestArtifactDataContentTypes(t *testing.T) {
	for name, want := range map[string]string{
		"a.json": "application/json; charset=utf-8",
		"a.md":   "text/plain; charset=utf-8",
		"a.txt":  "text/plain; charset=utf-8",
		"a.csv":  "text/csv; charset=utf-8",
		"a.html": "application/octet-stream",
		"a":      "application/octet-stream",
	} {
		assert.Equal(t, want, artifactDataContentType(name), name)
	}
}

func TestArtifactDataRejectsUnregisteredAndTraversal(t *testing.T) {
	e := newDataTestServer(t)
	f := e.write(t, filepath.Join(e.project, "ideas.json"), `{}`)
	e.write(t, filepath.Join(e.project, "other.json"), `{}`)
	id, w := e.publish(t, "s1", map[string]string{"ideas.json": f})
	require.Equal(t, 200, w.Code, w.Body.String())
	for _, p := range []string{
		"/artifacts/" + id + "/data/other.json",
		"/artifacts/" + id + "/data/..",
		"/artifacts/" + id + "/data/.ideas.json",
		"/artifacts/" + id + "/data/..%2Fideas.json",
		"/artifacts/" + id + "/data/%2e%2e%2fideas.json",
		"/artifacts/" + id + "/data/ideas.json%00",
		"/artifacts/" + strings.Repeat("0", 32) + "/data/ideas.json",
		"/artifacts/..%2F" + id + "/data/ideas.json",
	} {
		assert.Equal(t, 404, e.get("GET", p, nil).Code, p)
	}
	// An artifact without data files serves nothing.
	id2, w := e.publish(t, "s1", nil)
	require.Equal(t, 200, w.Code)
	assert.Equal(t, 404, e.get("GET", "/artifacts/"+id2+"/data/ideas.json", nil).Code)
}

func TestArtifactDataRejectedAtPublish(t *testing.T) {
	e := newDataTestServer(t)
	outside := e.write(t, filepath.Join(e.other, "secret.txt"), "s")
	link := filepath.Join(e.project, "link.txt")
	require.NoError(t, os.Symlink(outside, link))
	require.NoError(t, os.MkdirAll(filepath.Join(e.project, "dir"), 0o755))
	ok := e.write(t, filepath.Join(e.project, "ok.json"), "{}")
	secret := e.write(t, filepath.Join(e.home, ".agmux", "secrets", "k"), "x")
	cfg := e.write(t, filepath.Join(e.home, ".agmux", "config.toml"), "x")

	tooMany := map[string]string{}
	for i := 0; i < artifact.MaxDataFiles+1; i++ {
		tooMany[strings.Repeat("a", i+1)] = ok
	}
	cases := []struct {
		name    string
		session string
		files   map[string]string
		want    string
	}{
		{"outside project", "s1", map[string]string{"a": outside}, "inside the session's project directory"},
		{"symlink escaping project", "s1", map[string]string{"a": link}, "inside the session's project directory"},
		{"dotdot path", "s1", map[string]string{"a": e.project + "/../" + filepath.Base(e.other) + "/secret.txt"}, "inside the session's project directory"},
		{"relative path", "s1", map[string]string{"a": "ok.json"}, "absolute"},
		{"directory", "s1", map[string]string{"a": filepath.Join(e.project, "dir")}, "regular file"},
		{"missing", "s1", map[string]string{"a": filepath.Join(e.project, "nope")}, "cannot resolve"},
		{"agmux secrets inside project", "home", map[string]string{"a": secret}, "agmux data directory"},
		{"agmux config inside project", "home", map[string]string{"a": cfg}, "agmux data directory"},
		{"no project dir", "none", map[string]string{"a": ok}, "no project directory"},
		{"leading dot name", "s1", map[string]string{".a": ok}, "invalid name"},
		{"slash name", "s1", map[string]string{"x/a": ok}, "invalid name"},
		{"dotdot name", "s1", map[string]string{"..": ok}, "invalid name"},
		{"too many", "s1", tooMany, "at most 16"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before, _ := e.store.ListAll()
			_, w := e.publish(t, c.session, c.files)
			assert.Equal(t, 400, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), c.want)
			after, _ := e.store.ListAll()
			assert.Equal(t, len(before), len(after), "nothing registered")
		})
	}
}

func TestArtifactDataSymlinkSwapAfterPublish(t *testing.T) {
	e := newDataTestServer(t)
	f := e.write(t, filepath.Join(e.project, "ideas.json"), "{}")
	outside := e.write(t, filepath.Join(e.other, "secret.txt"), "secret")
	id, w := e.publish(t, "s1", map[string]string{"ideas.json": f})
	require.Equal(t, 200, w.Code, w.Body.String())

	require.NoError(t, os.Remove(f))
	require.NoError(t, os.Symlink(outside, f))
	r := e.get("GET", "/artifacts/"+id+"/data/ideas.json", nil)
	assert.Equal(t, 404, r.Code)
	assert.NotContains(t, r.Body.String(), "secret")

	// A symlink that stays inside the project is fine.
	inside := e.write(t, filepath.Join(e.project, "real.json"), `{"ok":1}`)
	require.NoError(t, os.Remove(f))
	require.NoError(t, os.Symlink(inside, f))
	r = e.get("GET", "/artifacts/"+id+"/data/ideas.json", nil)
	assert.Equal(t, 200, r.Code)
	assert.Equal(t, `{"ok":1}`, r.Body.String())
}

func TestArtifactDataSizeCap(t *testing.T) {
	e := newDataTestServer(t)
	f := e.write(t, filepath.Join(e.project, "big.json"), "{}")
	id, w := e.publish(t, "s1", map[string]string{"big.json": f})
	require.Equal(t, 200, w.Code)
	e.write(t, f, strings.Repeat("x", artifact.MaxDataSize))
	assert.Equal(t, 200, e.get("GET", "/artifacts/"+id+"/data/big.json", nil).Code)
	e.write(t, f, strings.Repeat("x", artifact.MaxDataSize+1))
	assert.Equal(t, 413, e.get("GET", "/artifacts/"+id+"/data/big.json", nil).Code)
}

func TestArtifactCSPWithDataFiles(t *testing.T) {
	e := newDataTestServer(t)
	plain, w := e.publish(t, "s1", nil)
	require.Equal(t, 200, w.Code)
	r := e.get("GET", "/artifacts/"+plain+"/raw", nil)
	require.Equal(t, 200, r.Code)
	assert.Equal(t, artifactCSP, r.Header().Get("Content-Security-Policy"), "unchanged without data files")
	assert.Contains(t, artifactCSP, "connect-src 'none'")

	f := e.write(t, filepath.Join(e.project, "ideas.json"), "{}")
	id, w := e.publish(t, "s1", map[string]string{"ideas.json": f})
	require.Equal(t, 200, w.Code)
	req := httptest.NewRequest("GET", "/artifacts/"+id+"/raw", nil)
	req.Host = "Phone.Example.ts.net:4321"
	rw := httptest.NewRecorder()
	e.h.ServeHTTP(rw, req)
	csp := rw.Header().Get("Content-Security-Policy")
	p := "//phone.example.ts.net:4321/artifacts/" + id + "/data/"
	assert.Contains(t, csp, "connect-src http:"+p+" https:"+p+";")
	assert.NotContains(t, csp, "connect-src 'none'")
	assert.True(t, strings.HasPrefix(csp, "sandbox allow-scripts;"))
	assert.Equal(t, strings.Replace(artifactCSP, "connect-src 'none'", "connect-src http:"+p+" https:"+p, 1), csp,
		"only connect-src differs")

	// A Host that can't be safely put in the header falls back to 'none'.
	for _, host := range []string{"a b", "x;script-src *", "[::1]:4321", ""} {
		req := httptest.NewRequest("GET", "/artifacts/"+id+"/raw", nil)
		req.Host = host
		rw := httptest.NewRecorder()
		e.h.ServeHTTP(rw, req)
		assert.Equal(t, artifactCSP, rw.Header().Get("Content-Security-Policy"), host)
	}
}

func TestUpdateArtifactDataFilesKeepReplaceClear(t *testing.T) {
	e := newDataTestServer(t)
	a := e.write(t, filepath.Join(e.project, "a.json"), "A")
	b := e.write(t, filepath.Join(e.project, "b.json"), "B")
	outside := e.write(t, filepath.Join(e.other, "x.json"), "X")
	id, w := e.publish(t, "s1", map[string]string{"a.json": a})
	require.Equal(t, 200, w.Code)
	names := func() []string {
		got, err := e.store.Get(id)
		require.NoError(t, err)
		return got.DataFiles
	}

	// Omitted (and null) keeps the mapping.
	require.Equal(t, 200, putArtifact(e.h, id, `{"title":"T2"}`).Code)
	assert.Equal(t, []string{"a.json"}, names())
	require.Equal(t, 200, putArtifact(e.h, id, `{"dataFiles":null}`).Code)
	assert.Equal(t, []string{"a.json"}, names())

	// Provided replaces it.
	body, _ := json.Marshal(map[string]interface{}{"sessionId": "s1", "dataFiles": map[string]string{"b.json": b}})
	require.Equal(t, 200, putArtifact(e.h, id, string(body)).Code)
	assert.Equal(t, []string{"b.json"}, names())
	assert.Equal(t, 404, e.get("GET", "/artifacts/"+id+"/data/a.json", nil).Code)
	assert.Equal(t, "B", e.get("GET", "/artifacts/"+id+"/data/b.json", nil).Body.String())

	// Invalid mapping is rejected and the old one is kept.
	body, _ = json.Marshal(map[string]interface{}{"dataFiles": map[string]string{"x.json": outside}})
	w = putArtifact(e.h, id, string(body))
	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "project directory")
	assert.Equal(t, []string{"b.json"}, names())

	// {} clears it.
	require.Equal(t, 200, putArtifact(e.h, id, `{"dataFiles":{}}`).Code)
	assert.Empty(t, names())
	assert.Equal(t, 404, e.get("GET", "/artifacts/"+id+"/data/b.json", nil).Code)
	r := e.get("GET", "/artifacts/"+id+"/raw", nil)
	assert.Equal(t, artifactCSP, r.Header().Get("Content-Security-Policy"))
}
