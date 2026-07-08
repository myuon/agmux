package session

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/myuon/agmux/internal/db"
)

// Injection points for tests. Production code always uses the defaults.
var (
	// reaperPSOutput lists all processes in "pid command" form.
	reaperPSOutput = func() ([]byte, error) {
		// -axo pid=,command= works on both macOS and Linux and suppresses headers.
		return exec.Command("ps", "-axo", "pid=,command=").Output()
	}

	// reaperKill sends a signal to a PID (negative PID targets a process group).
	reaperKill = func(pid int, sig syscall.Signal) error {
		return syscall.Kill(pid, sig)
	}

	// reaperIsAlive reports whether a PID is still running.
	reaperIsAlive = IsHolderAlive

	// reaperTermWait is how long to wait for SIGTERM'd holders to exit
	// before falling back to SIGKILL.
	reaperTermWait = 5 * time.Second
)

// orphanCandidate is a holder process found in the ps listing.
type orphanCandidate struct {
	pid       int
	sessionID string
	agmuxDir  string // value of --agmux-dir, empty for legacy holders
}

// parseHolderProcesses extracts holder processes from `ps -axo pid=,command=` output.
func parseHolderProcesses(psOut []byte) []orphanCandidate {
	var out []orphanCandidate
	for _, line := range strings.Split(string(psOut), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Fast filter: must look like an agmux holder invocation.
		if !strings.Contains(line, " holder --session-id ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		// fields[1] is argv[0] (the binary); it must be an agmux binary.
		if !strings.Contains(fields[1], "agmux") {
			continue
		}
		cand := orphanCandidate{pid: pid}
		// Scan flags up to the "--" separator (everything after it is the
		// wrapped CLI command and must not be interpreted as holder flags).
		args := fields[2:]
		for i := 0; i < len(args); i++ {
			if args[i] == "--" {
				break
			}
			switch args[i] {
			case "--session-id":
				if i+1 < len(args) {
					cand.sessionID = args[i+1]
					i++
				}
			case "--agmux-dir":
				if i+1 < len(args) {
					cand.agmuxDir = args[i+1]
					i++
				}
			}
		}
		if cand.sessionID == "" {
			continue
		}
		out = append(out, cand)
	}
	return out
}

// ReapOrphanHolders finds holder processes that are no longer referenced by
// this instance (not in the DB as any session's holder_pid and not managed by
// the in-memory stream process map) and terminates them.
//
// Background (#681): sessions.holder_pid stores only the latest PID per
// session. When a race (e.g. Restart vs SendKeys auto-recovery) overwrites the
// DB with a new PID, the previous holder becomes an orphan that nothing ever
// kills, accumulating across server restarts.
//
// ⚠️ CRITICAL kill ordering (see Delete() in manager.go and #441, #472, #502,
// #529, #569, #574): holders must be terminated by sending SIGTERM to the
// process group (-PID) first, waiting for graceful shutdown, and only then
// falling back to SIGKILL on the group. The holder's SIGTERM handler closes
// stdin so the child claude CLI exits cleanly; SIGKILL-first orphans the CLI.
// Holders are spawned with Setsid: true, so PID == PGID.
//
// This should be called after RecoverStreamProcesses so that holders which can
// be reconnected are re-referenced first and only truly orphaned ones remain.
func (m *Manager) ReapOrphanHolders() {
	selfDir, err := db.AgmuxDir()
	if err != nil {
		m.logger.Warn("reaper: cannot determine agmux dir; skipping orphan holder reaping", "error", err)
		return
	}

	psOut, err := reaperPSOutput()
	if err != nil {
		m.logger.Warn("reaper: ps failed; skipping orphan holder reaping", "error", err)
		return
	}
	candidates := parseHolderProcesses(psOut)
	if len(candidates) == 0 {
		return
	}

	// Holder PIDs currently managed in-memory (safety net in addition to the DB check).
	managed := make(map[int]struct{})
	m.streamMu.Lock()
	for _, sp := range m.streamProcesses {
		managed[sp.HolderPID()] = struct{}{}
	}
	m.streamMu.Unlock()

	selfPID := os.Getpid()
	var targets []orphanCandidate
	skipped := 0
	for _, c := range candidates {
		if c.pid <= 1 || c.pid == selfPID {
			skipped++
			continue
		}
		// Holders of another agmux instance must not be touched. Legacy holders
		// without --agmux-dir are treated as belonging to this instance.
		if c.agmuxDir != "" && c.agmuxDir != selfDir {
			m.logger.Info("reaper: skipping holder of another agmux instance",
				"sessionId", c.sessionID, "pid", c.pid, "agmuxDir", c.agmuxDir)
			skipped++
			continue
		}
		// A holder referenced by the DB as the session's current holder is legitimate.
		var dbPID int
		if err := m.db.QueryRow("SELECT holder_pid FROM sessions WHERE id = ?", c.sessionID).Scan(&dbPID); err == nil && dbPID == c.pid {
			skipped++
			continue
		}
		// Safety net: never kill a holder the in-memory map still manages.
		if _, ok := managed[c.pid]; ok {
			skipped++
			continue
		}
		targets = append(targets, c)
	}

	if len(targets) == 0 {
		m.logger.Info("reaper: no orphan holders found", "candidates", len(candidates), "skipped", skipped)
		return
	}

	// Batch phase 1: send SIGTERM to every orphan's process group first, then
	// wait once for all of them. With 100+ orphans, waiting serially per
	// process (3s each) would block startup for minutes.
	for _, t := range targets {
		m.logger.Info("reaper: terminating orphan holder (SIGTERM to process group)",
			"sessionId", t.sessionID, "pid", t.pid)
		if err := reaperKill(-t.pid, syscall.SIGTERM); err != nil {
			m.logger.Warn("reaper: SIGTERM failed", "sessionId", t.sessionID, "pid", t.pid, "error", err)
		}
	}

	// Batch phase 2: poll until all targets exit or the grace period expires.
	deadline := time.Now().Add(reaperTermWait)
	for time.Now().Before(deadline) {
		anyAlive := false
		for _, t := range targets {
			if reaperIsAlive(t.pid) {
				anyAlive = true
				break
			}
		}
		if !anyAlive {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Batch phase 3: SIGKILL the process groups of survivors.
	reaped := 0
	for _, t := range targets {
		if reaperIsAlive(t.pid) {
			m.logger.Info("reaper: holder survived SIGTERM, killing process group (SIGKILL fallback)",
				"sessionId", t.sessionID, "pid", t.pid)
			if err := reaperKill(-t.pid, syscall.SIGKILL); err != nil {
				m.logger.Warn("reaper: SIGKILL failed", "sessionId", t.sessionID, "pid", t.pid, "error", err)
			}
		}
		reaped++
	}

	m.logger.Info("reaper: orphan holder reaping done",
		"reaped", reaped, "skipped", skipped, "candidates", len(candidates))
}
