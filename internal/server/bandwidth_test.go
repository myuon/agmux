package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/go-chi/chi/v5"

	"github.com/myuon/agmux/internal/session"
)

type listSessions struct {
	session.SessionService
	all []session.Session
}

func (l *listSessions) List() ([]session.Session, error) { return l.all, nil }
func (l *listSessions) Get(id string) (*session.Session, error) {
	for i := range l.all {
		if l.all[i].ID == id {
			c := l.all[i]
			return &c, nil
		}
	}
	return nil, session.ErrSessionNotFound
}

func newListServer() (*Server, *chi.Mux) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := &listSessions{all: []session.Session{
		{ID: "a", InitialPrompt: "secret prompt"},
		{ID: "b", ParentSessionID: "a", InitialPrompt: "child prompt"},
		{ID: "c", ParentSessionID: "other"},
	}}
	s := &Server{sessions: svc, logger: logger, hub: NewHub(logger)}
	r := chi.NewRouter()
	r.Get("/api/sessions", s.listSessions)
	r.Get("/api/sessions/{id}", s.getSession)
	return s, r
}

func TestListSessionsParentFilter(t *testing.T) {
	_, r := newListServer()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions?parent=a", nil))
	var got []session.Session
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("got %+v", got)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions?parent=none", nil))
	var empty []session.Session
	if err := json.Unmarshal(w.Body.Bytes(), &empty); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("expected empty array, got %q", w.Body.String())
	}
}

func TestListOmitsInitialPromptSingleKeeps(t *testing.T) {
	_, r := newListServer()
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions", nil))
	var raw []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 3 {
		t.Fatalf("len=%d", len(raw))
	}
	for _, m := range raw {
		if _, ok := m["initialPrompt"]; ok {
			t.Fatalf("list contains initialPrompt: %v", m)
		}
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/sessions/a", nil))
	var one map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &one); err != nil {
		t.Fatal(err)
	}
	if one["initialPrompt"] != "secret prompt" {
		t.Fatalf("single GET lost initialPrompt: %v", one)
	}
}

func TestMountFrontendCacheControl(t *testing.T) {
	s, _ := newListServer()
	s.router = chi.NewRouter()
	s.MountFrontend(fstest.MapFS{
		"index.html":      {Data: []byte("<html>")},
		"assets/app-1.js": {Data: []byte("x")},
	})
	for path, want := range map[string]string{
		"/assets/app-1.js": "public, max-age=31536000, immutable",
		"/":                "no-cache",
		"/index.html":      "no-cache",
		"/some/spa/route":  "no-cache",
	} {
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if got := w.Header().Get("Cache-Control"); got != want {
			t.Errorf("%s: Cache-Control=%q want %q", path, got, want)
		}
	}
}
