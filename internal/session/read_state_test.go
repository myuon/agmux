package session

import (
	"testing"
	"time"
)

func readStateSession(t *testing.T) (*Manager, string) {
	t.Helper()
	m, rec := autoStopTestManager(t)
	s, err := m.Create("s", t.TempDir(), "", false, CreateOpts{})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_ = rec
	return m, s.ID
}

func unreadOf(t *testing.T, m *Manager, id string) bool {
	t.Helper()
	got, err := m.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Unread != m.IsUnread(id) {
		t.Fatalf("Get().Unread=%v but IsUnread=%v", got.Unread, m.IsUnread(id))
	}
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range list {
		if x.ID == id && x.Unread != got.Unread {
			t.Fatalf("List().Unread=%v but Get().Unread=%v", x.Unread, got.Unread)
		}
	}
	return got.Unread
}

// A session that never completed a turn (e.g. right after the migration) is read.
func TestUnread_NewSessionIsRead(t *testing.T) {
	m, id := readStateSession(t)
	if unreadOf(t, m, id) {
		t.Fatal("new session should be read")
	}
}

// working -> idle (turn completed) makes the session unread until MarkRead.
func TestUnread_TurnCompletedThenRead(t *testing.T) {
	m, id := readStateSession(t)
	if err := m.UpdateStatus(id, StatusWorking); err != nil {
		t.Fatal(err)
	}
	if unreadOf(t, m, id) {
		t.Fatal("working session should not be unread before the turn ends")
	}
	if err := m.UpdateStatus(id, StatusIdle); err != nil {
		t.Fatal(err)
	}
	if !unreadOf(t, m, id) {
		t.Fatal("turn completed: want unread")
	}
	time.Sleep(2 * time.Millisecond)
	if err := m.MarkRead(id); err != nil {
		t.Fatal(err)
	}
	if unreadOf(t, m, id) {
		t.Fatal("after MarkRead: want read")
	}
	// The next turn makes it unread again.
	time.Sleep(2 * time.Millisecond)
	_ = m.UpdateStatus(id, StatusWorking)
	_ = m.UpdateStatus(id, StatusIdle)
	if !unreadOf(t, m, id) {
		t.Fatal("second turn completed: want unread")
	}
}

// Transitions that are not a turn ending do not make the session unread.
func TestUnread_NonTurnTransitions(t *testing.T) {
	cases := []struct {
		name  string
		steps []Status
		want  bool
	}{
		{"idle to idle", []Status{StatusIdle, StatusIdle}, false},
		{"working to paused (user stop)", []Status{StatusWorking, StatusPaused}, false},
		{"working to archived", []Status{StatusWorking, StatusArchived}, false},
		{"working to working", []Status{StatusWorking, StatusWorking}, false},
		{"paused to idle", []Status{StatusPaused, StatusIdle}, false},
		{"working to waiting_input", []Status{StatusWorking, StatusWaitingInput}, true},
		{"working to exited", []Status{StatusWorking, StatusExited}, true},
		{"waiting_input to idle", []Status{StatusWaitingInput, StatusIdle}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, id := readStateSession(t)
			for _, st := range c.steps {
				if err := m.UpdateStatus(id, st); err != nil {
					t.Fatal(err)
				}
			}
			if got := unreadOf(t, m, id); got != c.want {
				t.Fatalf("unread=%v, want %v", got, c.want)
			}
		})
	}
}

// UpdateStatusWithError / UpdateStatusWithHolderPIDClear record turn ends too.
func TestUnread_OtherStatusSetters(t *testing.T) {
	m, id := readStateSession(t)
	_ = m.UpdateStatus(id, StatusWorking)
	if err := m.UpdateStatusWithError(id, StatusExited, "boom"); err != nil {
		t.Fatal(err)
	}
	if !unreadOf(t, m, id) {
		t.Fatal("UpdateStatusWithError: want unread")
	}

	m2, id2 := readStateSession(t)
	_ = m2.UpdateStatus(id2, StatusWorking)
	if err := m2.UpdateStatusWithHolderPIDClear(id2, StatusIdle); err != nil {
		t.Fatal(err)
	}
	if !unreadOf(t, m2, id2) {
		t.Fatal("UpdateStatusWithHolderPIDClear: want unread")
	}
}

func TestMarkRead_UnknownSession(t *testing.T) {
	m, _ := readStateSession(t)
	if err := m.MarkRead("nope"); err == nil {
		t.Fatal("want error for unknown session")
	}
	if m.IsUnread("nope") {
		t.Fatal("unknown session should be read")
	}
}
