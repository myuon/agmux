package session

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// killCall records a single call to the injected kill function.
type killCall struct {
	pid int
	sig syscall.Signal
}

// setupReaperTest swaps the reaper injection points for fakes and isolates
// HOME so db.AgmuxDir() never touches the real ~/.agmux. It returns the
// isolated agmux dir (the "self" instance dir) and a pointer to the recorded
// kill calls.
func setupReaperTest(t *testing.T, psLines string, alive func(pid int) bool) (selfDir string, calls *[]killCall) {
	t.Helper()

	home := t.TempDir()
	t.Setenv("HOME", home)
	selfDir = filepath.Join(home, ".agmux")

	origPS := reaperPSOutput
	origKill := reaperKill
	origAlive := reaperIsAlive
	origSock := reaperSocketExists
	origWait := reaperTermWait
	t.Cleanup(func() {
		reaperPSOutput = origPS
		reaperKill = origKill
		reaperIsAlive = origAlive
		reaperSocketExists = origSock
		reaperTermWait = origWait
	})

	reaperPSOutput = func() ([]byte, error) { return []byte(psLines), nil }

	recorded := []killCall{}
	calls = &recorded
	reaperKill = func(pid int, sig syscall.Signal) error {
		recorded = append(recorded, killCall{pid: pid, sig: sig})
		return nil
	}

	if alive == nil {
		// Default: every process dies immediately after SIGTERM.
		alive = func(pid int) bool { return false }
	}
	reaperIsAlive = alive
	// Default: every legacy holder has a socket in our socket dir, i.e. it
	// belongs to this instance. Tests for foreign legacy holders override this.
	reaperSocketExists = func(sessionID string) bool { return true }
	reaperTermWait = 200 * time.Millisecond

	return selfDir, calls
}

// insertReaperSession inserts a minimal session row with the given holder_pid.
func insertReaperSession(t *testing.T, db *sql.DB, id string, holderPID int) {
	t.Helper()
	now := time.Now()
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, project_path, tmux_session, status, type, output_mode, provider, model, holder_pid, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, "test", "/tmp", "", "idle", "worker", "stream", "claude", "", holderPID, now, now,
	)
	if err != nil {
		t.Fatalf("insert session %s: %v", id, err)
	}
}

// sigtermedPIDs returns the (positive) PIDs that received a process-group SIGTERM.
func sigtermedPIDs(calls []killCall) map[int]bool {
	out := map[int]bool{}
	for _, c := range calls {
		if c.sig == syscall.SIGTERM && c.pid < 0 {
			out[-c.pid] = true
		}
	}
	return out
}

func holderPSLine(pid int, sessionID, agmuxDir string) string {
	if agmuxDir == "" {
		return fmt.Sprintf("%5d /usr/local/bin/agmux holder --session-id %s --project-path /tmp/proj -- claude --model opus\n", pid, sessionID)
	}
	return fmt.Sprintf("%5d /usr/local/bin/agmux holder --session-id %s --project-path /tmp/proj --agmux-dir %s -- claude --model opus\n", pid, sessionID, agmuxDir)
}

// TestReapOrphanHolders_KeepsDBReferencedHolder: a holder whose PID matches
// the session's holder_pid in the DB is the legitimate holder and must NOT be reaped.
func TestReapOrphanHolders_KeepsDBReferencedHolder(t *testing.T) {
	ps := holderPSLine(90001, "sessA", "") +
		"  123 /sbin/launchd\n"
	_, calls := setupReaperTest(t, ps, nil)

	db := newTestDB(t)
	defer db.Close()
	insertReaperSession(t, db, "sessA", 90001)

	m := newTestManager(t, db)
	m.ReapOrphanHolders()

	if len(*calls) != 0 {
		t.Errorf("expected no kills for DB-referenced holder, got %v", *calls)
	}
}

// TestReapOrphanHolders_ReapsUnknownSession: a holder whose session no longer
// exists in the DB is an orphan and must be reaped via SIGTERM to its process group.
func TestReapOrphanHolders_ReapsUnknownSession(t *testing.T) {
	ps := holderPSLine(90002, "gone1", "")
	_, calls := setupReaperTest(t, ps, nil)

	db := newTestDB(t)
	defer db.Close()

	m := newTestManager(t, db)
	m.ReapOrphanHolders()

	termed := sigtermedPIDs(*calls)
	if !termed[90002] {
		t.Errorf("expected SIGTERM to process group -90002, got calls %v", *calls)
	}
	// SIGTERM must come first and target the process group (negative PID).
	if len(*calls) == 0 || (*calls)[0].sig != syscall.SIGTERM || (*calls)[0].pid != -90002 {
		t.Errorf("first kill must be SIGTERM to -90002, got %v", *calls)
	}
}

// TestReapOrphanHolders_ReapsDuplicateHolder: when two holders exist for the
// same session, only the one NOT referenced by the DB is reaped.
func TestReapOrphanHolders_ReapsDuplicateHolder(t *testing.T) {
	ps := holderPSLine(90010, "sessB", "") + // legitimate (matches DB)
		holderPSLine(90011, "sessB", "") // duplicate orphan
	_, calls := setupReaperTest(t, ps, nil)

	db := newTestDB(t)
	defer db.Close()
	insertReaperSession(t, db, "sessB", 90010)

	m := newTestManager(t, db)
	m.ReapOrphanHolders()

	termed := sigtermedPIDs(*calls)
	if termed[90010] {
		t.Errorf("DB-referenced holder 90010 must not be reaped; calls %v", *calls)
	}
	if !termed[90011] {
		t.Errorf("duplicate orphan holder 90011 must be reaped; calls %v", *calls)
	}
}

// TestReapOrphanHolders_SkipsOtherInstance: a holder started by another agmux
// instance (different --agmux-dir) must never be touched, even if this
// instance's DB does not know its session.
func TestReapOrphanHolders_SkipsOtherInstance(t *testing.T) {
	ps := holderPSLine(90020, "other", "/Users/someone/dev-home/.agmux")
	_, calls := setupReaperTest(t, ps, nil)

	db := newTestDB(t)
	defer db.Close()

	m := newTestManager(t, db)
	m.ReapOrphanHolders()

	if len(*calls) != 0 {
		t.Errorf("holder of another instance must not be killed, got %v", *calls)
	}
}

// TestReapOrphanHolders_ReapsOwnInstanceAndLegacy: holders whose --agmux-dir
// matches this instance, and legacy holders without --agmux-dir, are both
// reap targets when orphaned.
func TestReapOrphanHolders_ReapsOwnInstanceAndLegacy(t *testing.T) {
	// Set up first to learn the isolated self dir, then point the fake ps
	// output at holders carrying that exact --agmux-dir value.
	selfDir, calls := setupReaperTest(t, "", nil)
	ps := holderPSLine(90030, "own01", selfDir) + // own instance, orphaned
		holderPSLine(90031, "leg01", "") // legacy (no --agmux-dir), orphaned
	reaperPSOutput = func() ([]byte, error) { return []byte(ps), nil }

	db := newTestDB(t)
	defer db.Close()

	m := newTestManager(t, db)
	m.ReapOrphanHolders()

	termed := sigtermedPIDs(*calls)
	if !termed[90030] {
		t.Errorf("orphan holder with matching --agmux-dir must be reaped; calls %v", *calls)
	}
	if !termed[90031] {
		t.Errorf("legacy orphan holder without --agmux-dir must be reaped; calls %v", *calls)
	}
}

// TestReapOrphanHolders_ReapsLegacyWithSocket: a legacy holder (no --agmux-dir)
// whose Unix socket file exists in THIS instance's SocketDir belongs to this
// instance and is reaped when orphaned. Uses the real (os.Stat based) socket
// check against an actual file, with TMPDIR isolated to a temp dir.
func TestReapOrphanHolders_ReapsLegacyWithSocket(t *testing.T) {
	ps := holderPSLine(90060, "leg02", "")
	_, calls := setupReaperTest(t, ps, nil)

	// Isolate SocketDir (os.TempDir()/agmux/socks) and place a real socket file.
	t.Setenv("TMPDIR", t.TempDir())
	reaperSocketExists = defaultReaperSocketExists
	if err := os.MkdirAll(SocketDir(), 0700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	if err := os.WriteFile(SocketPath("leg02"), nil, 0600); err != nil {
		t.Fatalf("create socket file: %v", err)
	}

	db := newTestDB(t)
	defer db.Close()

	m := newTestManager(t, db)
	m.ReapOrphanHolders()

	if !sigtermedPIDs(*calls)[90060] {
		t.Errorf("legacy orphan holder with socket in our SocketDir must be reaped; calls %v", *calls)
	}
}

// TestReapOrphanHolders_SkipsLegacyWithoutSocket: a legacy holder with no
// socket file in THIS instance's SocketDir cannot be attributed to this
// instance (it may belong to an isolated dev server with a different
// HOME/TMPDIR) and must be skipped. Uses the real socket check against an
// isolated, empty SocketDir.
func TestReapOrphanHolders_SkipsLegacyWithoutSocket(t *testing.T) {
	ps := holderPSLine(90061, "leg03", "")
	_, calls := setupReaperTest(t, ps, nil)

	// Isolated SocketDir with NO socket file for leg03.
	t.Setenv("TMPDIR", t.TempDir())
	reaperSocketExists = defaultReaperSocketExists

	db := newTestDB(t)
	defer db.Close()

	m := newTestManager(t, db)
	m.ReapOrphanHolders()

	if len(*calls) != 0 {
		t.Errorf("legacy holder without a socket in our SocketDir must not be killed, got %v", *calls)
	}
}

// TestReapOrphanHolders_SkipsManagedStreamProcess: a holder PID present in the
// in-memory stream process map must not be reaped even if the DB row is missing.
func TestReapOrphanHolders_SkipsManagedStreamProcess(t *testing.T) {
	ps := holderPSLine(90040, "sessC", "")
	_, calls := setupReaperTest(t, ps, nil)

	db := newTestDB(t)
	defer db.Close()

	m := newTestManager(t, db)
	m.streamProcesses = map[string]*HolderStreamProcess{
		"sessC": {holderPID: 90040},
	}
	m.ReapOrphanHolders()

	if len(*calls) != 0 {
		t.Errorf("holder managed by streamProcesses must not be killed, got %v", *calls)
	}
}

// TestReapOrphanHolders_SIGKILLFallback: a holder that survives SIGTERM gets
// SIGKILL on its process group, and the ordering is SIGTERM first, SIGKILL last.
func TestReapOrphanHolders_SIGKILLFallback(t *testing.T) {
	ps := holderPSLine(90050, "stub1", "")
	alwaysAlive := func(pid int) bool { return true }
	_, calls := setupReaperTest(t, ps, alwaysAlive)

	db := newTestDB(t)
	defer db.Close()

	m := newTestManager(t, db)
	m.ReapOrphanHolders()

	got := *calls
	if len(got) != 2 {
		t.Fatalf("expected SIGTERM then SIGKILL, got %v", got)
	}
	if got[0].sig != syscall.SIGTERM || got[0].pid != -90050 {
		t.Errorf("first call must be SIGTERM to -90050, got %v", got[0])
	}
	if got[1].sig != syscall.SIGKILL || got[1].pid != -90050 {
		t.Errorf("second call must be SIGKILL to -90050, got %v", got[1])
	}
}

// TestParseHolderProcesses covers the ps output parsing edge cases.
func TestParseHolderProcesses(t *testing.T) {
	ps := "" +
		"    1 /sbin/launchd\n" +
		holderPSLine(101, "aaaaa", "") +
		holderPSLine(102, "bbbbb", "/home/x/.agmux") +
		"  300 grep agmux holder --session-id zzz\n" + // argv0 not agmux
		"  400 /usr/local/bin/agmux serve --port 4321\n" + // not a holder
		"  bad /usr/local/bin/agmux holder --session-id ccccc -- claude\n" // non-numeric pid

	got := parseHolderProcesses([]byte(ps))
	if len(got) != 2 {
		t.Fatalf("expected 2 holders, got %d: %+v", len(got), got)
	}
	if got[0].pid != 101 || got[0].sessionID != "aaaaa" || got[0].agmuxDir != "" {
		t.Errorf("holder[0] = %+v, want pid=101 session=aaaaa agmuxDir=\"\"", got[0])
	}
	if got[1].pid != 102 || got[1].sessionID != "bbbbb" || got[1].agmuxDir != "/home/x/.agmux" {
		t.Errorf("holder[1] = %+v, want pid=102 session=bbbbb agmuxDir=/home/x/.agmux", got[1])
	}
}
