package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRetentionDefaultsWhenSectionMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".agmux")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[server]\nport = 1234\n"), 0o644)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Retention != (RetentionConfig{14, 30, 30}) {
		t.Fatalf("got %+v", cfg.Retention)
	}
}

func TestRetentionRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := Default()
	cfg.Retention = RetentionConfig{LogDays: 0, OtelDays: 7, StreamDays: 3}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Retention != cfg.Retention {
		t.Fatalf("got %+v", got.Retention)
	}
}
