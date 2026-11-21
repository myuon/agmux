package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/myuon/agmux/internal/session"
)

// newReadStateTestServer is a projects test server backed by a real session
// manager, so status updates go through the same SQL as production (#859).
func newReadStateTestServer(t *testing.T) (*Server, *session.Manager) {
	t.Helper()
	s := newProjectsTestServer(t, false)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	m := session.NewManager(s.sqlDB, "claude", "", 0, logger, "test")
	s.sessions = m
	s.hub = NewHub(logger)
	return s, m
}

func listUnread(t *testing.T, s *Server) map[string]bool {
	t.Helper()
	rec := doJSON(t, s, http.MethodGet, "/api/sessions", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/sessions = %d: %s", rec.Code, rec.Body.String())
	}
	var list []struct {
		ID     string `json:"id"`
		Unread *bool  `json:"unread"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, x := range list {
		if x.Unread == nil {
			t.Fatalf("session %s has no unread field", x.ID)
		}
		out[x.ID] = *x.Unread
	}
	return out
}

func TestSessionRead_ListAndMarkRead(t *testing.T) {
	s, m := newReadStateTestServer(t)
	addSess(t, s, "a", "/p/x", 1, nil)
	addSess(t, s, "b", "/p/y", 2, nil)

	if got := listUnread(t, s); got["a"] || got["b"] {
		t.Fatalf("initially all read, got %v", got)
	}

	// A turn of "a" completes: only "a" becomes unread.
	if err := m.UpdateStatus("a", session.StatusWorking); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateStatus("a", session.StatusIdle); err != nil {
		t.Fatal(err)
	}
	if got := listUnread(t, s); !got["a"] || got["b"] {
		t.Fatalf("after turn of a: got %v", got)
	}

	rec := doJSON(t, s, http.MethodPost, "/api/sessions/a/read", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST read = %d: %s", rec.Code, rec.Body.String())
	}
	if got := listUnread(t, s); got["a"] {
		t.Fatalf("after read: got %v", got)
	}

	if rec := doJSON(t, s, http.MethodPost, "/api/sessions/nope/read", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("POST read unknown = %d, want 404", rec.Code)
	}
}

func TestSessionRead_ProjectCoordinatorAndMembers(t *testing.T) {
	s, m := newReadStateTestServer(t)
	path := "/p/proj"
	addSess(t, s, "coord", path, 10, nil)
	addSess(t, s, "w1", path, 5, map[string]string{"by": "coord"})
	addSess(t, s, "w2", path, 4, map[string]string{"by": "coord"})

	finish := func(id string) {
		t.Helper()
		if err := m.UpdateStatus(id, session.StatusWorking); err != nil {
			t.Fatal(err)
		}
		if err := m.UpdateStatus(id, session.StatusIdle); err != nil {
			t.Fatal(err)
		}
	}
	finish("coord")
	finish("w1")

	d := getProjectDetail(t, s, path)
	if d.Coordinator == nil || d.Coordinator.ID != "coord" {
		t.Fatalf("coordinator = %+v", d.Coordinator)
	}
	if !d.Coordinator.Unread {
		t.Error("coordinator should be unread")
	}
	if d.UnreadSessions != 1 {
		t.Errorf("unreadSessions = %d, want 1", d.UnreadSessions)
	}
	unread := map[string]bool{}
	for _, x := range d.Sessions {
		unread[x.ID] = x.Unread
	}
	if !unread["w1"] || unread["w2"] {
		t.Errorf("member unread = %v", unread)
	}

	_ = m.MarkRead("coord")
	_ = m.MarkRead("w1")
	d = getProjectDetail(t, s, path)
	if d.Coordinator.Unread || d.UnreadSessions != 0 {
		t.Errorf("after read: coordinator unread=%v unreadSessions=%d", d.Coordinator.Unread, d.UnreadSessions)
	}
}
