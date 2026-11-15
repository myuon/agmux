package secret

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCommandName(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		"gh":                "gh",
		"/usr/local/bin/gh": "gh",
		"./scripts/run.sh":  "run.sh",
	}
	for in, want := range cases {
		if got := CommandName(in); got != want {
			t.Errorf("CommandName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := CommandName(strings.Repeat("a", 500)); len(got) != maxCommandLength {
		t.Errorf("long command not truncated: len=%d", len(got))
	}
}

func TestStoreUsages(t *testing.T) {
	sqlDB, _ := openTestDB(t)
	st := NewStore(sqlDB)
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tick := 0
	st.now = func() time.Time {
		tick++
		return base.Add(time.Duration(tick) * time.Second)
	}
	if _, _, err := st.Upsert(Secret{Name: "A", Backend: BackendFile}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Upsert(Secret{Name: "B", Backend: BackendFile}); err != nil {
		t.Fatal(err)
	}

	// "used" for an unknown secret is rejected; "denied" is always recorded.
	if _, err := st.RecordUsage(Usage{SecretName: "NOPE"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("used unknown err = %v, want ErrNotFound", err)
	}

	zero, three := 0, 3
	if _, err := st.RecordUsage(Usage{SecretName: "A", SessionID: "s1", ProjectPath: "/p", Cwd: "/p/sub",
		Command: "/usr/bin/gh", ArgCount: 2, ExitCode: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordUsage(Usage{SecretName: "A", Command: "curl", ExitCode: &three}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordUsage(Usage{SecretName: "A", Status: UsageDenied, Command: "sh", ArgCount: 2, ExitCode: &three}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordUsage(Usage{SecretName: "B", Command: "env"}); err != nil {
		t.Fatal(err)
	}

	got, err := st.ListUsages("A", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3: %+v", len(got), got)
	}
	// Newest first.
	if got[0].Status != UsageDenied || got[0].ExitCode != nil || got[0].Command != "sh" {
		t.Fatalf("denied entry = %+v", got[0])
	}
	if got[1].Command != "curl" || got[1].ExitCode == nil || *got[1].ExitCode != 3 {
		t.Fatalf("second entry = %+v", got[1])
	}
	first := got[2]
	if first.Command != "gh" || first.ArgCount != 2 || first.SessionID != "s1" || first.ProjectPath != "/p" ||
		first.Cwd != "/p/sub" || first.ExitCode == nil || *first.ExitCode != 0 || first.Status != UsageUsed {
		t.Fatalf("first entry = %+v", first)
	}
	if !got[0].UsedAt.After(got[1].UsedAt) || !got[1].UsedAt.After(got[2].UsedAt) {
		t.Fatal("usages not ordered newest first")
	}

	// last_used_at follows "used" entries only.
	a, _ := st.Get("A")
	if a.LastUsedAt == nil || !a.LastUsedAt.Equal(got[1].UsedAt) {
		t.Fatalf("last_used_at = %v, want %v", a.LastUsedAt, got[1].UsedAt)
	}

	all, _ := st.ListUsages("", 0)
	if len(all) != 4 {
		t.Fatalf("all len = %d, want 4", len(all))
	}
	limited, _ := st.ListUsages("", 2)
	if len(limited) != 2 || limited[0].SecretName != "B" {
		t.Fatalf("limited = %+v", limited)
	}

	// Audit log survives deleting the secret.
	if err := st.Delete("A"); err != nil {
		t.Fatal(err)
	}
	if after, _ := st.ListUsages("A", 0); len(after) != 3 {
		t.Fatalf("usages after delete = %d, want 3", len(after))
	}
}
