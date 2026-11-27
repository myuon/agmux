package session

import (
	"bufio"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestHolderStreamStopKillsCLIOnTimeout checks that when the holder does not
// exit within the grace period, Stop kills the holder's whole process group so
// the CLI child does not keep running as an orphan.
func TestHolderStreamStopKillsCLIOnTimeout(t *testing.T) {
	orig := holderStopGracePeriod
	holderStopGracePeriod = 200 * time.Millisecond
	t.Cleanup(func() { holderStopGracePeriod = orig })

	// Stand-in for the holder: a shell in its own session (like SpawnHolder's
	// Setsid) that ignores the stop command and has a long-running child
	// standing in for the CLI.
	cmd := exec.Command("sh", "-c", "sleep 60 & echo $!; wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	server, client := net.Pipe()
	go func() {
		buf := make([]byte, 4096)
		for {
			if _, err := client.Read(buf); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { client.Close() })

	sp := &HolderStreamProcess{conn: server, done: done, holderPID: cmd.Process.Pid}

	stopped := make(chan struct{})
	go func() {
		sp.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return")
	}

	deadline := time.Now().Add(3 * time.Second)
	for syscall.Kill(childPID, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("CLI child %d is still running after Stop killed the holder", childPID)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
