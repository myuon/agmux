package session

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ExternalProcess represents a Claude or Codex process running outside of agmux.
type ExternalProcess struct {
	PID      int
	CWD      string
	Provider ProviderName
}

// ExternalDetector detects Claude processes running outside of agmux.
type ExternalDetector struct {
	mu        sync.RWMutex
	sessions  []Session
	knownTime map[string]time.Time // ID -> first seen CreatedAt
	logger    *slog.Logger
	stopCh    chan struct{}
	interval  time.Duration
}

// NewExternalDetector creates a new ExternalDetector.
func NewExternalDetector(logger *slog.Logger, interval time.Duration) *ExternalDetector {
	if logger == nil {
		logger = slog.Default()
	}
	return &ExternalDetector{
		logger:    logger.With("component", "external_detector"),
		knownTime: make(map[string]time.Time),
		stopCh:    make(chan struct{}),
		interval:  interval,
	}
}

// Start begins periodic detection of external Claude processes.
func (d *ExternalDetector) Start() {
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()

	// Run immediately once
	d.detect()

	for {
		select {
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.detect()
		}
	}
}

// Stop stops the detector.
func (d *ExternalDetector) Stop() {
	close(d.stopCh)
}

// Sessions returns the current list of external sessions.
func (d *ExternalDetector) Sessions() []Session {
	d.mu.RLock()
	defer d.mu.RUnlock()
	result := make([]Session, len(d.sessions))
	copy(result, d.sessions)
	return result
}

// detect finds external Claude and Codex processes and updates the sessions list.
func (d *ExternalDetector) detect() {
	processes, err := findExternalProcesses()
	if err != nil {
		d.logger.Error("failed to detect external processes", "error", err)
		return
	}

	now := time.Now()
	sessions := make([]Session, 0, len(processes))
	activeIDs := make(map[string]bool, len(processes))
	for _, p := range processes {
		id := fmt.Sprintf("ext-%d", p.PID)
		activeIDs[id] = true

		createdAt := now
		if t, ok := d.knownTime[id]; ok {
			createdAt = t
		} else {
			d.knownTime[id] = now
		}

		projectName := projectNameFromPath(p.CWD)
		s := Session{
			ID:          id,
			Name:        fmt.Sprintf("%s (pid:%d)", p.Provider, p.PID),
			ProjectPath: p.CWD,
			Status:      StatusWorking,
			Type:        TypeExternal,
			Provider:    p.Provider,
			CreatedAt:   createdAt,
			UpdatedAt:   now,
		}
		if projectName != "" {
			s.Name = fmt.Sprintf("%s (%s external)", projectName, p.Provider)
		}
		sessions = append(sessions, s)
	}

	// Clean up stale entries from knownTime
	for id := range d.knownTime {
		if !activeIDs[id] {
			delete(d.knownTime, id)
		}
	}

	d.mu.Lock()
	d.sessions = sessions
	d.mu.Unlock()
}

// findExternalProcesses finds Claude and Codex processes not managed by this agmux instance.
func findExternalProcesses() ([]ExternalProcess, error) {
	myPID := os.Getpid()

	// Get all processes: ps -eo pid,ppid,comm
	out, err := exec.Command("ps", "-eo", "pid,ppid,comm").Output()
	if err != nil {
		return nil, fmt.Errorf("ps command failed: %w", err)
	}

	// Collect all PIDs in the agmux process tree
	agmuxPIDs := collectProcessTree(myPID, string(out))

	type pidWithProvider struct {
		PID      int
		Provider ProviderName
	}
	var externalPIDs []pidWithProvider
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		comm := fields[2]
		provider := detectProvider(comm)
		if provider == "" {
			continue
		}
		// Skip if this process is in the agmux tree
		if agmuxPIDs[pid] {
			continue
		}
		externalPIDs = append(externalPIDs, pidWithProvider{PID: pid, Provider: provider})
	}

	if len(externalPIDs) == 0 {
		return nil, nil
	}

	// Get CWD for each external process via lsof
	var results []ExternalProcess
	for _, p := range externalPIDs {
		cwd := getCWD(p.PID)
		results = append(results, ExternalProcess{
			PID:      p.PID,
			CWD:      cwd,
			Provider: p.Provider,
		})
	}

	return results, nil
}

// collectProcessTree collects all PIDs that are descendants of rootPID.
func collectProcessTree(rootPID int, psOutput string) map[int]bool {
	// Build parent-to-children map
	children := make(map[int][]int)
	for _, line := range strings.Split(psOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err1 := strconv.Atoi(fields[0])
		ppid, err2 := strconv.Atoi(fields[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
	}

	// BFS from rootPID
	result := map[int]bool{rootPID: true}
	queue := []int{rootPID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, child := range children[current] {
			if !result[child] {
				result[child] = true
				queue = append(queue, child)
			}
		}
	}
	return result
}

// isClaudeProcess checks if a process name corresponds to Claude CLI.
func isClaudeProcess(comm string) bool {
	return detectProvider(comm) == ProviderClaude
}

// isCodexProcess checks if a process name corresponds to Codex CLI.
func isCodexProcess(comm string) bool {
	return detectProvider(comm) == ProviderCodex
}

// detectProvider returns the provider name for a given process command name.
// Returns empty string if the process is not a recognized AI agent.
func detectProvider(comm string) ProviderName {
	base := comm
	if idx := strings.LastIndex(comm, "/"); idx >= 0 {
		base = comm[idx+1:]
	}
	switch base {
	case "claude":
		return ProviderClaude
	case "codex":
		return ProviderCodex
	default:
		return ""
	}
}

// getCWD returns the current working directory of a process.
func getCWD(pid int) string {
	out, err := exec.Command("lsof", "-p", strconv.Itoa(pid), "-Fn", "-a", "-d", "cwd").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") && len(line) > 1 {
			return line[1:]
		}
	}
	return ""
}

// projectNameFromPath extracts the last component of a path as a project name.
func projectNameFromPath(path string) string {
	if path == "" {
		return ""
	}
	path = strings.TrimRight(path, "/")
	if idx := strings.LastIndex(path, "/"); idx >= 0 {
		return path[idx+1:]
	}
	return path
}
