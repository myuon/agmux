package session

import (
	"database/sql"
	"testing"
	"time"
)

func querySessionLiveness(t *testing.T, db *sql.DB, id string) (status string, holderPID int, lastError string) {
	t.Helper()
	if err := db.QueryRow("SELECT status, holder_pid, COALESCE(last_error, '') FROM sessions WHERE id = ?", id).
		Scan(&status, &holderPID, &lastError); err != nil {
		t.Fatalf("query session %s: %v", id, err)
	}
	return
}

// TestStopAllStreamProcesses_KeepsStatusAndHolderPID verifies #717: the
// intentional disconnect on server shutdown must not be reported as a process
// exit. Otherwise status=exited / holder_pid=0 is written and the next server's
// orphan reaper kills the still-alive holder.
func TestStopAllStreamProcesses_KeepsStatusAndHolderPID(t *testing.T) {
	m, db := newSwitchModelTestManager(t)

	ids := []string{"shut-a", "shut-b", "shut-c", "shut-d"}
	holders := make([]*fakeHolder, 0, len(ids))
	for _, id := range ids {
		insertSwitchModelSession(t, db, id, "claude", "working", "", 1)
		if _, err := db.Exec("UPDATE sessions SET holder_pid = ? WHERE id = ?", fakeHolderPID, id); err != nil {
			t.Fatalf("set holder_pid: %v", err)
		}
		holders = append(holders, attachFakeHolder(t, m, id, StreamOpts{}))
	}

	m.StopAllStreamProcesses()

	for i, id := range ids {
		select {
		case <-holders[i].sp.Done():
		default:
			t.Errorf("%s: read loop still running after StopAllStreamProcesses returned", id)
			// Let it finish so any (buggy) exit callback has a chance to hit the DB.
			select {
			case <-holders[i].sp.Done():
			case <-time.After(3 * time.Second):
				t.Fatalf("%s: read loop did not exit", id)
			}
		}
		select {
		case <-holders[i].stopped:
			t.Errorf("%s: holder received a stop command; it must stay alive", id)
		default:
		}
		status, pid, lastErr := querySessionLiveness(t, db, id)
		if status != "working" {
			t.Errorf("%s: status = %q, want %q", id, status, "working")
		}
		if pid != fakeHolderPID {
			t.Errorf("%s: holder_pid = %d, want %d", id, pid, fakeHolderPID)
		}
		if lastErr != "" {
			t.Errorf("%s: last_error = %q, want empty", id, lastErr)
		}
	}
}

// TestHolderConnLost_StillMarksExited verifies that a genuine holder loss
// (the holder closes its end) keeps the existing behavior of marking the
// session exited and clearing holder_pid.
func TestHolderConnLost_StillMarksExited(t *testing.T) {
	m, db := newSwitchModelTestManager(t)
	const id = "lost-holder"
	insertSwitchModelSession(t, db, id, "claude", "working", "", 1)
	if _, err := db.Exec("UPDATE sessions SET holder_pid = ? WHERE id = ?", fakeHolderPID, id); err != nil {
		t.Fatalf("set holder_pid: %v", err)
	}
	fh := attachFakeHolder(t, m, id, StreamOpts{})

	_ = fh.server.Close()

	select {
	case <-fh.sp.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("read loop did not exit after holder closed")
	}
	// The exit callback runs before done is closed, so the DB is already updated.
	status, pid, lastErr := querySessionLiveness(t, db, id)
	if status != string(StatusExited) {
		t.Errorf("status = %q, want %q", status, StatusExited)
	}
	if pid != 0 {
		t.Errorf("holder_pid = %d, want 0", pid)
	}
	if lastErr == "" {
		t.Error("last_error should be set for an unexpected holder loss")
	}
	m.streamMu.Lock()
	_, still := m.streamProcesses[id]
	m.streamMu.Unlock()
	if still {
		t.Error("stream process should be removed from the map")
	}
}
