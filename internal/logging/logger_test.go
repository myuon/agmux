package logging

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewWriterSkipsStderrWhenSameFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agmux.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	// Simulate launchd redirecting stderr to the same log file.
	stderr, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()

	if _, err := io.WriteString(newWriter(stderr, file, stderrIsFile(stderr, file.Name())), "line\n"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(path)
	if got := strings.Count(string(b), "line\n"); got != 1 {
		t.Fatalf("expected 1 line, got %d: %q", got, b)
	}
}

func TestNewWriterWritesBothWhenDifferentFiles(t *testing.T) {
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, "agmux.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stderr, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()

	if _, err := io.WriteString(newWriter(stderr, file, stderrIsFile(stderr, file.Name())), "line\n"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{file.Name(), stderr.Name()} {
		b, _ := os.ReadFile(p)
		if string(b) != "line\n" {
			t.Fatalf("%s: got %q", p, b)
		}
	}
}
