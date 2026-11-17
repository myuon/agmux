package experimental

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func withFeatures(t *testing.T, fs ...Feature) {
	t.Helper()
	orig := Features
	Features = fs
	t.Cleanup(func() { Features = orig })
	t.Setenv("HOME", t.TempDir())
	cache.mu.Lock()
	cache.loaded = false
	cache.mu.Unlock()
}

func TestDefaultOff(t *testing.T) {
	withFeatures(t, Feature{Name: "foo"})
	if Enabled("foo") {
		t.Fatal("expected foo to be off by default")
	}
	if Enabled("unknown") {
		t.Fatal("unknown feature must be off")
	}
}

func TestSetPersistsAndPreservesOtherConfig(t *testing.T) {
	withFeatures(t, Feature{Name: "foo"}, Feature{Name: "bar"})
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".agmux")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[server]\nport = 5555\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Set("foo", true); err != nil {
		t.Fatal(err)
	}
	if !Enabled("foo") || Enabled("bar") {
		t.Fatalf("unexpected state: foo=%v bar=%v", Enabled("foo"), Enabled("bar"))
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if !contains(string(data), "port = 5555") || !contains(string(data), "foo = true") {
		t.Fatalf("config not preserved/persisted:\n%s", data)
	}

	if err := Set("foo", false); err != nil {
		t.Fatal(err)
	}
	if Enabled("foo") {
		t.Fatal("foo should be off")
	}
	if err := Set("nope", true); err == nil {
		t.Fatal("expected error for unknown feature")
	}
}

func TestListReflectsState(t *testing.T) {
	withFeatures(t, Feature{Name: "foo", Title: "Foo"})
	if err := Set("foo", true); err != nil {
		t.Fatal(err)
	}
	l := List()
	if len(l) != 1 || !l[0].Enabled || l[0].Title != "Foo" {
		t.Fatalf("unexpected list: %+v", l)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
