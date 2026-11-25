package server

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/myuon/agmux/internal/session"
)

type archiveSessions struct {
	session.SessionService
	archived []string
}

func (a *archiveSessions) Archive(id string) error {
	if id == "missing" {
		return fmt.Errorf("%w: %s", session.ErrSessionNotFound, id)
	}
	if id == "boom" {
		return fmt.Errorf("boom")
	}
	a.archived = append(a.archived, id)
	return nil
}

func (a *archiveSessions) List() ([]session.Session, error) { return nil, nil }

func TestArchiveSessionEndpoint(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := &archiveSessions{}
	s := &Server{sessions: svc, logger: logger, hub: NewHub(logger)}
	r := chi.NewRouter()
	r.Post("/api/sessions/{id}/archive", s.archiveSession)

	for _, tc := range []struct {
		id   string
		want int
	}{{"s1", http.StatusOK}, {"missing", http.StatusNotFound}, {"boom", http.StatusInternalServerError}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/api/sessions/"+tc.id+"/archive", nil))
		if w.Code != tc.want {
			t.Errorf("%s: got %d want %d (%s)", tc.id, w.Code, tc.want, w.Body.String())
		}
	}
	if len(svc.archived) != 1 || svc.archived[0] != "s1" {
		t.Fatalf("archived = %v", svc.archived)
	}
}
