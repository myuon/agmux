package logging

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func listNames(dir string) []string {
	es, _ := os.ReadDir(dir)
	var n []string
	for _, e := range es {
		n = append(n, e.Name())
	}
	return n
}

func TestRotateOnDateChange(t *testing.T) {
	dir := t.TempDir()
	c := &clock{time.Date(2026, 11, 1, 23, 59, 0, 0, time.Local)}
	r, err := openRotating(dir, rotateOptions{now: c.now})
	if err != nil {
		t.Fatal(err)
	}
	r.Write([]byte("day1\n"))
	c.t = time.Date(2026, 11, 2, 0, 0, 1, 0, time.Local)
	r.Write([]byte("day2\n"))
	r.Close()

	b, _ := os.ReadFile(filepath.Join(dir, "agmux.log"))
	if string(b) != "day2\n" {
		t.Fatalf("current log: %q", b)
	}
	gz := filepath.Join(dir, "agmux-20261102-000001.log.gz")
	f, err := os.Open(gz)
	if err != nil {
		t.Fatalf("missing gz: %v (%v)", err, listNames(dir))
	}
	defer f.Close()
	zr, _ := gzip.NewReader(f)
	data, _ := io.ReadAll(zr)
	if string(data) != "day1\n" {
		t.Fatalf("gz content: %q", data)
	}
	if _, err := os.Stat(strings.TrimSuffix(gz, ".gz")); err == nil {
		t.Fatal("plain rotated file should be removed")
	}
}

func TestRotateOnSize(t *testing.T) {
	dir := t.TempDir()
	c := &clock{time.Date(2026, 11, 1, 10, 0, 0, 0, time.Local)}
	r, _ := openRotating(dir, rotateOptions{now: c.now, maxSize: 10})
	r.Write([]byte("123456\n"))
	c.t = c.t.Add(time.Second)
	r.Write([]byte("abcdef\n")) // would exceed 10 bytes -> rotate
	r.Close()
	b, _ := os.ReadFile(filepath.Join(dir, "agmux.log"))
	if string(b) != "abcdef\n" {
		t.Fatalf("current: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "agmux-20261101-100001.log.gz")); err != nil {
		t.Fatal(listNames(dir))
	}
}

func TestStartupRotatesStaleLog(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agmux.log")
	os.WriteFile(p, []byte("old\n"), 0o644)
	old := time.Now().Add(-48 * time.Hour)
	os.Chtimes(p, old, old)
	r, err := openRotating(dir, rotateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r.Write([]byte("new\n"))
	r.Close()
	b, _ := os.ReadFile(p)
	if string(b) != "new\n" {
		t.Fatalf("current: %q", b)
	}
	gzs, _ := filepath.Glob(filepath.Join(dir, "agmux-*.log.gz"))
	if len(gzs) != 1 {
		t.Fatal(listNames(dir))
	}
}

func TestStartupKeepsTodaysLog(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agmux.log")
	os.WriteFile(p, []byte("today\n"), 0o644)
	r, _ := openRotating(dir, rotateOptions{})
	r.Write([]byte("more\n"))
	r.Close()
	b, _ := os.ReadFile(p)
	if string(b) != "today\nmore\n" {
		t.Fatalf("current: %q", b)
	}
}

func TestPruneOldRotatedLogs(t *testing.T) {
	dir := t.TempDir()
	oldGz := filepath.Join(dir, "agmux-20260101-000000.log.gz")
	newGz := filepath.Join(dir, "agmux-20261030-000000.log.gz")
	for _, p := range []string{oldGz, newGz} {
		os.WriteFile(p, []byte("x"), 0o644)
	}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local)
	os.Chtimes(oldGz, now.AddDate(0, 0, -20), now.AddDate(0, 0, -20))
	os.Chtimes(newGz, now.AddDate(0, 0, -2), now.AddDate(0, 0, -2))
	c := &clock{now}
	r, _ := openRotating(dir, rotateOptions{now: c.now, days: 14})
	r.Close()
	if _, err := os.Stat(oldGz); err == nil {
		t.Fatal("old gz should be pruned")
	}
	if _, err := os.Stat(newGz); err != nil {
		t.Fatal("recent gz should remain")
	}

	// days <= 0 never deletes
	os.WriteFile(oldGz, []byte("x"), 0o644)
	os.Chtimes(oldGz, now.AddDate(0, 0, -400), now.AddDate(0, 0, -400))
	r, _ = openRotating(dir, rotateOptions{now: c.now, days: 0})
	r.Close()
	if _, err := os.Stat(oldGz); err != nil {
		t.Fatal("days=0 must keep logs")
	}
}

func TestStderrIsFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "agmux.log")
	f, _ := os.Create(p)
	defer f.Close()
	if !stderrIsFile(f, p) {
		t.Fatal("expected same file")
	}
	g, _ := os.Create(filepath.Join(dir, "other"))
	defer g.Close()
	if stderrIsFile(g, p) {
		t.Fatal("expected different file")
	}
}
