package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAndMigrate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := Open(dbPath)
	require.NoError(t, err)
	defer db.Close()

	_, err = os.Stat(dbPath)
	assert.NoError(t, err)

	// Verify tables exist
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='sessions'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 1, count)

	// daemon_actions table should NOT exist (dropped)
	err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='daemon_actions'").Scan(&count)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestDBPathForPort(t *testing.T) {
	tests := []struct {
		name     string
		port     int
		wantFile string
	}{
		{"default port returns agmux.db", 4321, "agmux.db"},
		{"custom port returns agmux-<port>.db", 5000, "agmux-5000.db"},
		{"another custom port", 8080, "agmux-8080.db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, err := DBPathForPort(tt.port)
			require.NoError(t, err)
			assert.Equal(t, tt.wantFile, filepath.Base(path))
			assert.Contains(t, path, ".agmux")
		})
	}
}

func TestMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db1, err := Open(dbPath)
	require.NoError(t, err)
	db1.Close()

	db2, err := Open(dbPath)
	require.NoError(t, err)
	db2.Close()
}
