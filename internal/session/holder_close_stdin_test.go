package session

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBuildHolderArgs_CloseStdinFlag(t *testing.T) {
	off := buildHolderArgs("s1", []string{"claude", "--x"}, "/p", "/a", false)
	want := []string{"holder", "--session-id", "s1", "--project-path", "/p", "--agmux-dir", "/a", "--", "claude", "--x"}
	if !reflect.DeepEqual(off, want) {
		t.Errorf("got %v want %v", off, want)
	}

	on := buildHolderArgs("s1", []string{"codex", "exec"}, "/p", "/a", true)
	want = []string{"holder", "--session-id", "s1", "--project-path", "/p", "--agmux-dir", "/a", "--close-stdin", "--", "codex", "exec"}
	if !reflect.DeepEqual(on, want) {
		t.Errorf("got %v want %v", on, want)
	}
}

// With closeStdin the child sees EOF on stdin, so a CLI that reads stdin to
// EOF (like codex exec >= 0.171.0 with a piped stdin) terminates.
func TestRunHolder_CloseStdinLetsStdinReaderExit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	tmp, err := os.MkdirTemp("/tmp", "agh")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	t.Setenv("TMPDIR", tmp)

	run := func(id string, closeStdin bool) chan error {
		ch := make(chan error, 1)
		go func() {
			ch <- RunHolder(id, []string{"sh", "-c", "cat >/dev/null; echo done"}, tmp, os.Environ(), closeStdin)
		}()
		return ch
	}

	select {
	case err := <-run("closed", true):
		if err != nil {
			t.Fatalf("RunHolder: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("holder did not exit although stdin was closed")
	}

	b, err := os.ReadFile(filepath.Join(home, ".agmux", "streams", "closed.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "done") {
		t.Errorf("stream missing child output: %q", b)
	}

	// Without closeStdin the child keeps waiting on stdin.
	ch := run("open", false)
	select {
	case <-ch:
		t.Fatal("holder exited although stdin was left open")
	case <-time.After(1500 * time.Millisecond):
	}
	// leave the goroutine; the child is killed when the test process exits
}
