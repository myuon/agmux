package session

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func insertNamedSession(t *testing.T, db *sql.DB, id, name, nameSource string) {
	t.Helper()
	now := time.Now()
	_, err := db.Exec(
		`INSERT INTO sessions (id, name, name_source, project_path, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, name, nameSource, "/tmp", "idle", now, now,
	)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
}

func queryName(t *testing.T, db *sql.DB, id string) (string, string) {
	t.Helper()
	var name, source string
	if err := db.QueryRow("SELECT name, name_source FROM sessions WHERE id = ?", id).Scan(&name, &source); err != nil {
		t.Fatalf("query name: %v", err)
	}
	return name, source
}

func stubGenerator(title string, err error) (TitleGenerator, *int) {
	calls := 0
	return func(ctx context.Context, message string) (string, error) {
		calls++
		return title, err
	}, &calls
}

func TestRename_SetsManualAndPersists(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertNamedSession(t, db, "s1", "agmux", NameSourceDefault)
	m := newTestManager(t, db)

	var notified string
	m.SetOnNameChange(func(id, name string) { notified = name })

	if err := m.Rename("s1", "  新しい名前  "); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	name, source := queryName(t, db, "s1")
	if name != "新しい名前" || source != NameSourceManual {
		t.Errorf("got (%q, %q), want (%q, %q)", name, source, "新しい名前", NameSourceManual)
	}
	if notified != "新しい名前" {
		t.Errorf("onNameChange not fired with new name, got %q", notified)
	}
	s, err := m.Get("s1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if s.Name != "新しい名前" || s.NameSource != NameSourceManual {
		t.Errorf("Get returned (%q, %q)", s.Name, s.NameSource)
	}
}

func TestRename_RejectsEmptyAndUnknown(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertNamedSession(t, db, "s1", "agmux", NameSourceManual)
	m := newTestManager(t, db)

	if err := m.Rename("s1", "   "); err == nil {
		t.Error("expected error for whitespace name")
	}
	if err := m.Rename("missing", "x"); err == nil {
		t.Error("expected error for unknown session")
	}
	if name, _ := queryName(t, db, "s1"); name != "agmux" {
		t.Errorf("name changed to %q", name)
	}
}

func TestAutoTitle_AppliesOnlyWhenPending(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertNamedSession(t, db, "pending", "agmux", NameSourceDefault)
	insertNamedSession(t, db, "manual", "my session", NameSourceManual)
	insertNamedSession(t, db, "auto", "既存タイトル", NameSourceAuto)
	m := newTestManager(t, db)
	gen, calls := stubGenerator("「ログイン画面のバグ修正」。\n", nil)
	m.SetTitleGenerator(gen)

	for _, id := range []string{"pending", "manual", "auto"} {
		if m.claimAutoTitle(id) {
			m.runAutoTitle(id, "ログイン画面でエラーが出るので直して")
		}
	}

	if *calls != 1 {
		t.Errorf("generator called %d times, want 1", *calls)
	}
	if name, source := queryName(t, db, "pending"); name != "ログイン画面のバグ修正" || source != NameSourceAuto {
		t.Errorf("pending: got (%q, %q)", name, source)
	}
	if name, source := queryName(t, db, "manual"); name != "my session" || source != NameSourceManual {
		t.Errorf("manual: got (%q, %q)", name, source)
	}
	if name, source := queryName(t, db, "auto"); name != "既存タイトル" || source != NameSourceAuto {
		t.Errorf("auto: got (%q, %q)", name, source)
	}

	// Once auto-titled, later messages do not re-title.
	if m.claimAutoTitle("pending") {
		t.Error("auto-titled session must not be claimed again")
	}
}

func TestAutoTitle_DoesNotOverwriteManualRename(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertNamedSession(t, db, "s1", "agmux", NameSourceDefault)
	m := newTestManager(t, db)

	if !m.claimAutoTitle("s1") {
		t.Fatal("expected claim to succeed")
	}
	// User renames while generation is in flight.
	m.SetTitleGenerator(func(ctx context.Context, message string) (string, error) {
		if err := m.Rename("s1", "手動の名前"); err != nil {
			t.Fatalf("Rename: %v", err)
		}
		return "自動タイトル", nil
	})
	m.runAutoTitle("s1", "hello")

	if name, source := queryName(t, db, "s1"); name != "手動の名前" || source != NameSourceManual {
		t.Errorf("got (%q, %q), want manual rename preserved", name, source)
	}
	if m.claimAutoTitle("s1") {
		t.Error("manually renamed session must not be claimed")
	}
}

func TestAutoTitle_GeneratorErrorKeepsName(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()
	insertNamedSession(t, db, "s1", "agmux", NameSourceDefault)
	m := newTestManager(t, db)
	fired := false
	m.SetOnNameChange(func(id, name string) { fired = true })

	for _, gen := range []TitleGenerator{
		func(ctx context.Context, message string) (string, error) { return "", errors.New("boom") },
		func(ctx context.Context, message string) (string, error) { return "  \n", nil },
	} {
		m.SetTitleGenerator(gen)
		if !m.claimAutoTitle("s1") {
			t.Fatal("expected claim to succeed")
		}
		m.runAutoTitle("s1", "hello")
		if name, source := queryName(t, db, "s1"); name != "agmux" || source != NameSourceDefault {
			t.Errorf("got (%q, %q), want name kept and still pending", name, source)
		}
	}
	if fired {
		t.Error("onNameChange must not fire on failure")
	}
}

func TestSanitizeTitle(t *testing.T) {
	cases := map[string]string{
		"Title: \"Fix login bug\"\n": "Fix login bug",
		"『設定画面の改善』。":                 "設定画面の改善",
		"  multi   space  ":          "multi space",
		"first line\nsecond line":    "first line",
		"**Bold title**":             "Bold title",
		"":                           "",
	}
	for in, want := range cases {
		if got := sanitizeTitle(in); got != want {
			t.Errorf("sanitizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}
