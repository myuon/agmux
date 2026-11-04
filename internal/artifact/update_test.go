package artifact

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/myuon/agmux/internal/db"
)

func newTestStore(t *testing.T) (*Store, string, string) {
	t.Helper()
	d := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(d, "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { sqlDB.Close() })
	dir := filepath.Join(d, "artifacts")
	st := NewStore(sqlDB, dir)
	src := writeFile(t, filepath.Join(d, "a.html"), "<p>v1</p>")
	id, err := Publish(dir, src)
	require.NoError(t, err)
	_, err = st.Register("s1", id, "T1")
	require.NoError(t, err)
	return st, id, d
}

func parseTime(t *testing.T, v string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339Nano, v)
	require.NoError(t, err)
	return ts
}

func readStored(t *testing.T, st *Store, id string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(st.Dir(), id, FileName))
	require.NoError(t, err)
	return string(b)
}

func dirEntries(t *testing.T, st *Store, id string) []string {
	t.Helper()
	es, err := os.ReadDir(filepath.Join(st.Dir(), id))
	require.NoError(t, err)
	var names []string
	for _, e := range es {
		names = append(names, e.Name())
	}
	return names
}

func TestReplaceKeepsIdAndMetadata(t *testing.T) {
	st, id, d := newTestStore(t)
	before, err := st.Get(id)
	require.NoError(t, err)
	assert.Equal(t, before.CreatedAt, before.UpdatedAt)

	v2 := writeFile(t, filepath.Join(d, "b.html"), "<p>v2</p>")
	rep, err := Replace(st.Dir(), id, v2)
	require.NoError(t, err)
	assert.Len(t, dirEntries(t, st, id), 2, "backup exists until committed")
	a, err := st.Touch(id, "")
	require.NoError(t, err)
	rep.Commit()

	assert.Equal(t, "<p>v2</p>", readStored(t, st, id))
	assert.Equal(t, []string{FileName}, dirEntries(t, st, id), "no temp/backup files left")
	assert.Equal(t, id, a.ID)
	assert.Equal(t, before.CreatedAt, a.CreatedAt)
	assert.Equal(t, "s1", a.SessionID)
	assert.Equal(t, "T1", a.Title, "title kept when omitted")
	assert.True(t, parseTime(t, a.UpdatedAt).After(parseTime(t, before.UpdatedAt)))

	a2, err := st.Touch(id, "  T2 ")
	require.NoError(t, err)
	assert.Equal(t, "T2", a2.Title)
	assert.True(t, parseTime(t, a2.UpdatedAt).After(parseTime(t, a.UpdatedAt)))
	list, err := st.List("s1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, a2.UpdatedAt, list[0].UpdatedAt)
	assert.Equal(t, "s1", list[0].SessionID)
}

func TestReplaceRollback(t *testing.T) {
	st, id, d := newTestStore(t)
	v2 := writeFile(t, filepath.Join(d, "b.html"), "<p>v2</p>")
	rep, err := Replace(st.Dir(), id, v2)
	require.NoError(t, err)
	require.NoError(t, rep.Rollback())
	assert.Equal(t, "<p>v1</p>", readStored(t, st, id))
	assert.Equal(t, []string{FileName}, dirEntries(t, st, id))
}

func TestReplaceFailuresLeaveOldContent(t *testing.T) {
	st, id, d := newTestStore(t)
	txt := writeFile(t, filepath.Join(d, "b.txt"), "x")
	big := filepath.Join(d, "big.html")
	f, err := os.Create(big)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(MaxSize+1))
	f.Close()
	for name, src := range map[string]string{
		"wrong ext": txt, "oversize": big, "missing": filepath.Join(d, "nope.html"), "relative": "a.html",
	} {
		_, err := Replace(st.Dir(), id, src)
		assert.Error(t, err, name)
		assert.Equal(t, "<p>v1</p>", readStored(t, st, id), name)
		assert.Equal(t, []string{FileName}, dirEntries(t, st, id), name)
	}
}

func TestReplaceUnknownAndTraversalWriteNothing(t *testing.T) {
	st, id, d := newTestStore(t)
	v2 := writeFile(t, filepath.Join(d, "b.html"), "<p>v2</p>")
	victim := filepath.Join(d, "victim")
	require.NoError(t, os.Mkdir(victim, 0o755))

	unknown := strings.Repeat("0", 32)
	for _, bad := range []string{unknown, "../victim", "..", "", id + "/..", filepath.Join("..", "victim")} {
		_, err := Replace(st.Dir(), bad, v2)
		assert.ErrorIs(t, err, ErrNotFound, bad)
	}
	_, err := os.Stat(filepath.Join(st.Dir(), unknown))
	assert.True(t, os.IsNotExist(err), "unknown id must not create a directory")
	es, _ := os.ReadDir(victim)
	assert.Empty(t, es)
	_, err = st.Touch(unknown, "x")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = st.Touch("../victim", "x")
	assert.ErrorIs(t, err, ErrNotFound)
	assert.Equal(t, "<p>v1</p>", readStored(t, st, id))
}

func TestConcurrentReplaceIsAtomic(t *testing.T) {
	st, id, d := newTestStore(t)
	a := strings.Repeat("A", 1<<16)
	b := strings.Repeat("B", 1<<16)
	srcA := writeFile(t, filepath.Join(d, "a2.html"), a)
	srcB := writeFile(t, filepath.Join(d, "b2.html"), b)

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { // every observation must be wholly one version
		defer close(done)
		p := filepath.Join(st.Dir(), id, FileName)
		for {
			select {
			case <-stop:
				return
			default:
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				t.Errorf("read: %v", err)
				return
			}
			if got := string(raw); got != "<p>v1</p>" && got != a && got != b {
				t.Errorf("torn read of %d bytes", len(got))
				return
			}
		}
	}()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			src := srcA
			if i%2 == 1 {
				src = srcB
			}
			unlock := LockUpdate(id)
			defer unlock()
			rep, err := Replace(st.Dir(), id, src)
			if !assert.NoError(t, err) {
				return
			}
			rep.Commit()
		}(i)
	}
	wg.Wait()
	close(stop)
	<-done
	assert.Equal(t, []string{FileName}, dirEntries(t, st, id))
}

func TestMigrationAddsUpdatedAtToOldDB(t *testing.T) {
	d := t.TempDir()
	path := filepath.Join(d, "old.db")
	raw, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = raw.Exec(`CREATE TABLE artifacts (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, title TEXT NOT NULL, created_at TEXT NOT NULL)`)
	require.NoError(t, err)
	id := strings.Repeat("b", 32)
	_, err = raw.Exec(`INSERT INTO artifacts VALUES (?, 's1', 'Old', '2026-01-02T03:04:05Z')`, id)
	require.NoError(t, err)
	raw.Close()

	for i := 0; i < 2; i++ { // second open proves the migration is idempotent
		sqlDB, err := db.Open(path)
		require.NoError(t, err)
		sqlDB.Close()
	}
	sqlDB, err := db.Open(path)
	require.NoError(t, err)
	defer sqlDB.Close()
	st := NewStore(sqlDB, filepath.Join(d, "artifacts"))
	a, err := st.Get(id)
	require.NoError(t, err)
	assert.Equal(t, "2026-01-02T03:04:05Z", a.UpdatedAt, "old rows fall back to created_at")
	list, err := st.List("s1")
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "2026-01-02T03:04:05Z", list[0].UpdatedAt)
}
