package secret

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/myuon/agmux/internal/db"
)

func TestValidateName(t *testing.T) {
	ok := []string{"A", "_", "GITHUB_TOKEN", "_X1", "A9_B"}
	for _, n := range ok {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{"", "lower", "1ABC", "A-B", "A B", "A.B", "Ä", "A\n", strings.Repeat("A", MaxNameLength+1), "../X"}
	for _, n := range bad {
		if err := ValidateName(n); err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", n)
		}
	}
}

func TestValidateBackendKind(t *testing.T) {
	if err := ValidateBackendKind(BackendKeychain); err != nil {
		t.Error(err)
	}
	if err := ValidateBackendKind(BackendFile); err != nil {
		t.Error(err)
	}
	if err := ValidateBackendKind(Backend1Password); err == nil {
		t.Error("1password should not be supported yet")
	}
	if err := ValidateBackendKind("plaintext"); err == nil {
		t.Error("unknown backend accepted")
	}
}

func TestProjectAllowed(t *testing.T) {
	cases := []struct {
		allowed []string
		project string
		want    bool
	}{
		{nil, "/any", true},
		{[]string{}, "", true},
		{[]string{"/p/a"}, "/p/a", true},
		{[]string{"/p/a/"}, "/p/a", true},
		{[]string{"/p/a"}, "/p/a/.claude/worktrees/x", true},
		{[]string{"/p/a"}, "/p/ab", false},
		{[]string{"/p/a"}, "/p", false},
		{[]string{"/p/a"}, "", false},
		{[]string{"/p/a", "/q"}, "/q/z", true},
	}
	for _, c := range cases {
		if got := ProjectAllowed(c.allowed, c.project); got != c.want {
			t.Errorf("ProjectAllowed(%v, %q) = %v, want %v", c.allowed, c.project, got, c.want)
		}
	}
}

func TestFileBackendPermsAndRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	b := NewFileBackend(dir)
	ctx := context.Background()
	val := fakeToken("file", 6) + " with space\nand newline"

	if err := b.Set(ctx, "MY_SECRET", val); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Errorf("dir perm = %o, want 700", st.Mode().Perm())
	}
	fst, err := os.Stat(filepath.Join(dir, "MY_SECRET"))
	if err != nil {
		t.Fatal(err)
	}
	if fst.Mode().Perm() != 0o600 {
		t.Errorf("file perm = %o, want 600", fst.Mode().Perm())
	}
	got, err := b.Get(ctx, &Secret{Name: "MY_SECRET"})
	if err != nil || got != val {
		t.Fatalf("Get = %q, %v", got, err)
	}
	// Overwrite.
	if err := b.Set(ctx, "MY_SECRET", "v2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Get(ctx, &Secret{Name: "MY_SECRET"}); got != "v2" {
		t.Fatalf("after overwrite Get = %q", got)
	}
	// No leftover temp files.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want 1", len(entries))
	}
	if err := b.Delete(ctx, "MY_SECRET"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, &Secret{Name: "MY_SECRET"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete err = %v, want ErrNotFound", err)
	}
	if err := b.Delete(ctx, "MY_SECRET"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing err = %v", err)
	}
	// Invalid names cannot escape the dir.
	if err := b.Set(ctx, "../ESCAPE", "x"); err == nil {
		t.Fatal("path traversal name accepted")
	}
}

func TestFileBackendFixesDirPerms(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "secrets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := NewFileBackend(dir).Set(context.Background(), "A", "v"); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(dir)
	if st.Mode().Perm() != 0o700 {
		t.Errorf("dir perm = %o, want 700", st.Mode().Perm())
	}
}

func openTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	sqlDB, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return sqlDB, path
}

func TestStoreCRUD(t *testing.T) {
	sqlDB, _ := openTestDB(t)
	st := NewStore(sqlDB)

	list, err := st.List()
	if err != nil || len(list) != 0 {
		t.Fatalf("List = %v, %v", list, err)
	}
	s, created, err := st.Upsert(Secret{Name: "TOKEN", Description: "d", Backend: BackendFile, AllowedProjects: []string{"/p"}})
	if err != nil || !created {
		t.Fatalf("Upsert = %v, %v, %v", s, created, err)
	}
	if s.Backend != BackendFile || len(s.AllowedProjects) != 1 || s.LastUsedAt != nil || s.CreatedAt.IsZero() {
		t.Fatalf("unexpected secret %+v", s)
	}
	s2, created, err := st.Upsert(Secret{Name: "TOKEN", Description: "d2", Backend: BackendKeychain})
	if err != nil || created {
		t.Fatalf("second Upsert created=%v err=%v", created, err)
	}
	if s2.Description != "d2" || s2.Backend != BackendKeychain || len(s2.AllowedProjects) != 0 || !s2.CreatedAt.Equal(s.CreatedAt) {
		t.Fatalf("unexpected updated secret %+v", s2)
	}
	if err := st.MarkUsed("TOKEN"); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Get("TOKEN")
	if got.LastUsedAt == nil {
		t.Fatal("LastUsedAt not set")
	}
	if err := st.MarkUsed("NOPE"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkUsed missing = %v", err)
	}
	if _, _, err := st.Upsert(Secret{Name: "bad-name"}); err == nil {
		t.Fatal("invalid name accepted")
	}
	if err := st.Delete("TOKEN"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Get("TOKEN"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete = %v", err)
	}
	if err := st.Delete("TOKEN"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing = %v", err)
	}
}

// fakeRunner emulates security(1) for KeychainBackend tests.
type fakeRunner struct {
	items map[string]string // account -> stored password (as security sees it)
	calls [][]string
	stdin []string
}

func (f *fakeRunner) Run(_ context.Context, stdin []byte, args ...string) ([]byte, int, error) {
	f.calls = append(f.calls, args)
	f.stdin = append(f.stdin, string(stdin))
	switch args[0] {
	case "-i":
		// Minimal parse of: add-generic-password -U -s SVC -a NAME -w VALUE
		fields := strings.Fields(string(stdin))
		if len(fields) != 8 || fields[0] != "add-generic-password" || fields[3] != KeychainService {
			return nil, 1, nil
		}
		f.items[fields[5]] = fields[7]
		return nil, 0, nil
	case "find-generic-password":
		v, ok := f.items[args[4]]
		if !ok {
			return nil, securityExitNotFound, nil
		}
		return []byte(v + "\n"), 0, nil
	case "delete-generic-password":
		if _, ok := f.items[args[4]]; !ok {
			return nil, securityExitNotFound, nil
		}
		delete(f.items, args[4])
		return nil, 0, nil
	}
	return nil, 2, nil
}

func TestKeychainBackendWithFakeRunner(t *testing.T) {
	f := &fakeRunner{items: map[string]string{}}
	b := NewKeychainBackend(f)
	ctx := context.Background()
	// Value with characters that would need quoting in `security -i` input.
	val := fakeToken("kc", 4) + ` sp"q'\ 日本語` + "\t"

	if err := b.Set(ctx, "KC_SECRET", val); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		for _, a := range c {
			if strings.Contains(a, val) || strings.Contains(a, "agmux-b64:") {
				t.Fatalf("value leaked into argv: %v", c)
			}
		}
	}
	if !strings.HasPrefix(f.stdin[0], "add-generic-password -U -s agmux-secret -a KC_SECRET -w agmux-b64:") {
		t.Fatalf("unexpected stdin command: %q", f.stdin[0])
	}
	if strings.Contains(f.stdin[0], val) {
		t.Fatal("raw value written to security stdin (should be encoded)")
	}
	got, err := b.Get(ctx, &Secret{Name: "KC_SECRET"})
	if err != nil || got != val {
		t.Fatalf("Get = %q, %v", got, err)
	}
	// Items not written by agmux are returned as-is.
	f.items["RAW"] = "plain"
	if got, _ := b.Get(ctx, &Secret{Name: "RAW"}); got != "plain" {
		t.Fatalf("raw Get = %q", got)
	}
	if err := b.Delete(ctx, "KC_SECRET"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Get(ctx, &Secret{Name: "KC_SECRET"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete = %v", err)
	}
	if err := b.Delete(ctx, "KC_SECRET"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing = %v", err)
	}
	if err := b.Set(ctx, "BIG", strings.Repeat("x", MaxKeychainValueLength+1)); err == nil {
		t.Fatal("oversized value accepted")
	}
	if err := b.Set(ctx, "bad name", "x"); err == nil {
		t.Fatal("invalid name accepted")
	}
}

func TestKeychainBackendSetFailureDoesNotLeakValue(t *testing.T) {
	b := NewKeychainBackend(failingRunner{})
	val := fakeToken("leak", 4)
	err := b.Set(context.Background(), "X", val)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), val) {
		t.Fatalf("error contains value: %v", err)
	}
}

type failingRunner struct{}

func (failingRunner) Run(context.Context, []byte, ...string) ([]byte, int, error) {
	return nil, 1, nil
}

// TestKeychainBackendReal exercises the real login keychain via
// /usr/bin/security. Opt-in: AGMUX_TEST_REAL_KEYCHAIN=1.
func TestKeychainBackendReal(t *testing.T) {
	if os.Getenv("AGMUX_TEST_REAL_KEYCHAIN") != "1" {
		t.Skip("set AGMUX_TEST_REAL_KEYCHAIN=1 to run against the real keychain")
	}
	b := NewKeychainBackend(NewSecurityRunner())
	ctx := context.Background()
	name := "AGMUX_TEST_REAL_KEYCHAIN_ITEM"
	t.Cleanup(func() { _ = b.Delete(ctx, name) })
	val := fakeToken("real", 5) + ` with "quotes" 'and' \back\slash 日本語`
	if err := b.Set(ctx, name, val); err != nil {
		t.Fatal(err)
	}
	got, err := b.Get(ctx, &Secret{Name: name})
	if err != nil || got != val {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := b.Delete(ctx, name); err != nil {
		t.Fatal(err)
	}
}

// TestStoreNeverHoldsValue checks that registering metadata + value through
// the backend leaves no trace of the value in the DB file.
func TestStoreNeverHoldsValue(t *testing.T) {
	sqlDB, path := openTestDB(t)
	st := NewStore(sqlDB)
	fb := NewFileBackend(filepath.Join(t.TempDir(), "secrets"))
	val := fakeToken("dbleak", 6)
	if _, _, err := st.Upsert(Secret{Name: "DB_CHECK", Backend: BackendFile}); err != nil {
		t.Fatal(err)
	}
	if err := fb.Set(context.Background(), "DB_CHECK", val); err != nil {
		t.Fatal(err)
	}
	_ = st.MarkUsed("DB_CHECK")
	_, _ = sqlDB.Exec(`PRAGMA wal_checkpoint(FULL)`)
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if bytes.Contains(data, []byte(val)) {
			t.Fatalf("value found in %s", p)
		}
	}
}
