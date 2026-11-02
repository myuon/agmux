package session

import "testing"

func TestFireStatusChangeFiresSessionEndOnlyForEndingStatuses(t *testing.T) {
	m := &Manager{}
	var ended []string
	var changes int
	m.SetOnSessionEnd(func(id string) { ended = append(ended, id) })
	m.SetOnStatusChange(func(string, Status, string) { changes++ })

	for _, st := range []Status{StatusIdle, StatusWorking} {
		m.fireStatusChange("s1", st, "")
	}
	if len(ended) != 0 {
		t.Fatalf("session end fired for a live status: %v", ended)
	}
	for _, st := range []Status{StatusPaused, StatusExited, StatusArchived} {
		m.fireStatusChange("s1", st, "")
	}
	if len(ended) != 3 || changes != 5 {
		t.Fatalf("ended=%v changes=%d", ended, changes)
	}
}
