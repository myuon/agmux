package retention

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/config"
	"github.com/myuon/agmux/internal/db"
)

func newCleaner(t *testing.T, cfg config.RetentionConfig) (*Cleaner, *sql.DB, string) {
	t.Helper()
	dir := t.TempDir()
	d, err := db.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	streams := filepath.Join(dir, "streams")
	os.MkdirAll(streams, 0o755)
	c := New(d, streams, func() config.RetentionConfig { return cfg }, nil)
	c.BatchSleep = 0
	return c, d, streams
}

func count(t *testing.T, d *sql.DB, table string) int {
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPruneOtel(t *testing.T) {
	c, d, _ := newCleaner(t, config.RetentionConfig{OtelDays: 30})
	now := time.Now()
	old := now.AddDate(0, 0, -40)
	recent := now.AddDate(0, 0, -5)
	for i := 0; i < 3; i++ {
		d.Exec(`INSERT INTO otel_events (name, timestamp) VALUES ('e', ?)`, old)
		d.Exec(`INSERT INTO otel_metrics (name, value, timestamp) VALUES ('m', 1, ?)`, old)
	}
	d.Exec(`INSERT INTO otel_events (name, timestamp) VALUES ('e', ?)`, recent)
	d.Exec(`INSERT INTO otel_metrics (name, value, timestamp) VALUES ('m', 1, ?)`, recent)

	c.RunOnce(context.Background())
	if n := count(t, d, "otel_events"); n != 1 {
		t.Fatalf("otel_events = %d, want 1", n)
	}
	if n := count(t, d, "otel_metrics"); n != 1 {
		t.Fatalf("otel_metrics = %d, want 1", n)
	}
}

func TestPruneOtelDisabled(t *testing.T) {
	c, d, _ := newCleaner(t, config.RetentionConfig{OtelDays: 0})
	d.Exec(`INSERT INTO otel_events (name, timestamp) VALUES ('e', ?)`, time.Now().AddDate(-1, 0, 0))
	c.RunOnce(context.Background())
	if n := count(t, d, "otel_events"); n != 1 {
		t.Fatalf("otel_events = %d, want 1", n)
	}
}

func TestPruneStreams(t *testing.T) {
	c, d, streams := newCleaner(t, config.RetentionConfig{StreamDays: 30})
	for id, status := range map[string]string{
		"archived-old": "archived", "archived-new": "archived",
		"paused-old": "paused", "exited-old": "exited",
		"working-old": "working", "idle-old": "idle", "waiting-old": "waiting_input",
	} {
		if _, err := d.Exec(`INSERT INTO sessions (id, name, project_path, status) VALUES (?, ?, '/tmp', ?)`, id, id, status); err != nil {
			t.Fatal(err)
		}
	}
	oldT := time.Now().AddDate(0, 0, -40)
	files := map[string]bool{ // name -> old?
		"archived-old": true, "archived-new": false, "paused-old": true, "exited-old": true,
		"working-old": true, "idle-old": true, "waiting-old": true,
		"missing-old": true, "missing-new": false,
	}
	for id, old := range files {
		p := filepath.Join(streams, id+".jsonl")
		os.WriteFile(p, []byte("{}\n"), 0o644)
		if old {
			os.Chtimes(p, oldT, oldT)
		}
	}
	os.WriteFile(filepath.Join(streams, "note.txt"), []byte("x"), 0o644)
	os.Chtimes(filepath.Join(streams, "note.txt"), oldT, oldT)

	c.RunOnce(context.Background())

	wantGone := map[string]bool{"archived-old": true, "paused-old": true, "exited-old": true, "missing-old": true}
	for id := range files {
		_, err := os.Stat(filepath.Join(streams, id+".jsonl"))
		gone := os.IsNotExist(err)
		if gone != wantGone[id] {
			t.Errorf("%s: gone=%v, want %v", id, gone, wantGone[id])
		}
	}
	if _, err := os.Stat(filepath.Join(streams, "note.txt")); err != nil {
		t.Error("non-jsonl file must not be touched")
	}
}

func TestPruneStreamsDisabled(t *testing.T) {
	c, _, streams := newCleaner(t, config.RetentionConfig{StreamDays: 0})
	p := filepath.Join(streams, "gone.jsonl")
	os.WriteFile(p, []byte("{}\n"), 0o644)
	old := time.Now().AddDate(-1, 0, 0)
	os.Chtimes(p, old, old)
	c.RunOnce(context.Background())
	if _, err := os.Stat(p); err != nil {
		t.Fatal("stream_days=0 must keep files")
	}
}
