package session

import (
	"fmt"
	"time"
)

// Read/unread state (#859).
//
// sessions.last_turn_completed_at records (Unix ms) when a turn last ended,
// i.e. the status left a turn (working / waiting_input) for one where the
// agent stops and the result is there to look at (idle / waiting_input /
// exited). sessions.last_read_at records when the user last had the session
// screen open. A session is unread while last_turn_completed_at >
// last_read_at. Both default to 0, so sessions that never completed a turn
// since the columns were added count as read.

// turnCompletedSet is the SET fragment status updates prepend so that the
// turn-end time is recorded in the same statement as the new status. In an
// UPDATE, `status` still refers to the old value. Its arguments (new status,
// new status, now in Unix ms) come from turnCompletedArgs.
const turnCompletedSet = `last_turn_completed_at = CASE
	WHEN status IN ('working', 'waiting_input') AND status <> ? AND ? IN ('idle', 'waiting_input', 'exited') THEN ?
	ELSE last_turn_completed_at END`

// turnCompletedArgs returns the arguments of turnCompletedSet followed by rest.
func turnCompletedArgs(status Status, now time.Time, rest ...any) []any {
	return append([]any{string(status), string(status), now.UnixMilli()}, rest...)
}

// MarkRead records that the user has seen the session now.
func (m *Manager) MarkRead(id string) error {
	res, err := m.db.Exec("UPDATE sessions SET last_read_at = ? WHERE id = ?", time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("session not found: %s", id)
	}
	return nil
}

// IsUnread reports whether the session has a completed turn the user has not
// seen yet. Unknown sessions are reported as read.
func (m *Manager) IsUnread(id string) bool {
	var unread bool
	if err := m.db.QueryRow("SELECT last_turn_completed_at > last_read_at FROM sessions WHERE id = ?", id).Scan(&unread); err != nil {
		return false
	}
	return unread
}
