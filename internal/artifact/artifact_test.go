package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/myuon/agmux/internal/db"
)

func writeFile(t *testing.T, p, content string) string {
	t.Helper()
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}

func TestValidateSource(t *testing.T) {
	d := t.TempDir()
	ok := writeFile(t, filepath.Join(d, "a.html"), "<p>x</p>")
	writeFile(t, filepath.Join(d, "b.txt"), "x")
	require.NoError(t, os.Mkdir(filepath.Join(d, "dir.html"), 0o755))
	require.NoError(t, os.Symlink(filepath.Join(d, "b.txt"), filepath.Join(d, "link-to-txt.html")))
	require.NoError(t, os.Symlink(ok, filepath.Join(d, "link-to-html.htm")))
	big := filepath.Join(d, "big.html")
	f, err := os.Create(big)
	require.NoError(t, err)
	require.NoError(t, f.Truncate(MaxSize+1))
	f.Close()

	_, err = ValidateSource(ok)
	assert.NoError(t, err)
	_, err = ValidateSource(filepath.Join(d, "link-to-html.htm"))
	assert.NoError(t, err, "symlink to a real html file is fine")

	for name, p := range map[string]string{
		"wrong extension":      filepath.Join(d, "b.txt"),
		"symlink to wrong ext": filepath.Join(d, "link-to-txt.html"),
		"directory":            filepath.Join(d, "dir.html"),
		"oversize":             big,
		"missing":              filepath.Join(d, "nope.html"),
		"relative":             "a.html",
	} {
		_, err := ValidateSource(p)
		assert.Error(t, err, name)
	}
}

func TestPublishCopiesSnapshot(t *testing.T) {
	d := t.TempDir()
	src := writeFile(t, filepath.Join(d, "a.html"), "<p>v1</p>")
	dir := filepath.Join(d, "artifacts")
	id, err := Publish(dir, src)
	require.NoError(t, err)
	assert.True(t, ValidID(id))
	writeFile(t, src, "<p>v2</p>")
	got, err := os.ReadFile(filepath.Join(dir, id, FileName))
	require.NoError(t, err)
	assert.Equal(t, "<p>v1</p>", string(got))
	info, err := os.Stat(filepath.Join(dir, id))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestValidIDRejectsTraversal(t *testing.T) {
	good := strings.Repeat("a", 32)
	assert.True(t, ValidID(good))
	for _, id := range []string{"", "..", "../x", good + "/..", strings.Repeat("A", 32), good[:31], good + "0", "a/b", `..\x`, good + "\n"} {
		assert.False(t, ValidID(id), id)
	}
}

func TestStoreDeleteOnlyWithinDir(t *testing.T) {
	d := t.TempDir()
	sqlDB, err := db.Open(filepath.Join(d, "t.db"))
	require.NoError(t, err)
	defer sqlDB.Close()
	dir := filepath.Join(d, "artifacts")
	st := NewStore(sqlDB, dir)

	// A sibling directory that a traversal id would target.
	victim := filepath.Join(d, "victim")
	require.NoError(t, os.Mkdir(victim, 0o755))
	writeFile(t, filepath.Join(victim, "keep"), "x")

	src := writeFile(t, filepath.Join(d, "a.html"), "<p>x</p>")
	id, err := Publish(dir, src)
	require.NoError(t, err)
	_, err = st.Register("s1", id, "  ")
	assert.Error(t, err, "empty title")
	_, err = st.Register("s1", id, "T")
	require.NoError(t, err)
	_, err = st.Register("s1", "../victim", "T")
	assert.Error(t, err)

	for _, bad := range []string{"../victim", "..", ".", "", filepath.Join("..", "victim")} {
		assert.Error(t, st.Delete(bad), bad)
		assert.Error(t, RemoveFiles(dir, bad), bad)
	}
	_, err = os.Stat(filepath.Join(victim, "keep"))
	assert.NoError(t, err)
	_, err = os.Stat(dir)
	assert.NoError(t, err)

	list, err := st.List("s1")
	require.NoError(t, err)
	require.Len(t, list, 1)

	require.NoError(t, st.Delete(id))
	_, err = os.Stat(filepath.Join(dir, id))
	assert.True(t, os.IsNotExist(err))
	_, err = st.Get(id)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = os.Stat(dir)
	assert.NoError(t, err, "artifacts root survives")
}
