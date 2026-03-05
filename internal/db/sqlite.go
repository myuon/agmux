package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

func AgmuxDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, ".agmux")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create dir: %w", err)
	}
	return dir, nil
}

func DefaultDBPath() (string, error) {
	dir, err := AgmuxDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "agmux.db"), nil
}

func StreamsDir() (string, error) {
	dir, err := AgmuxDir()
	if err != nil {
		return "", err
	}
	streamsDir := filepath.Join(dir, "streams")
	if err := os.MkdirAll(streamsDir, 0o755); err != nil {
		return "", fmt.Errorf("create streams dir: %w", err)
	}
	return streamsDir, nil
}

func ControllerDir() (string, error) {
	dir, err := AgmuxDir()
	if err != nil {
		return "", err
	}
	controllerDir := filepath.Join(dir, "controller")
	if err := os.MkdirAll(controllerDir, 0o755); err != nil {
		return "", fmt.Errorf("create controller dir: %w", err)
	}
	return controllerDir, nil
}

func Open(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			project_path TEXT NOT NULL,
			initial_prompt TEXT,
			tmux_session TEXT NOT NULL UNIQUE,
			status TEXT NOT NULL DEFAULT 'working',
			type TEXT NOT NULL DEFAULT 'worker',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);

		DROP TABLE IF EXISTS daemon_actions;
	`)
	if err != nil {
		return err
	}

	// Migration: add type column if missing (for existing databases)
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN type TEXT NOT NULL DEFAULT 'worker'`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: add output_mode column if missing
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN output_mode TEXT NOT NULL DEFAULT 'terminal'`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: add current_task column if missing
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN current_task TEXT`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: add goal column if missing
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN goal TEXT`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: update old status values to new ones
	_, err = db.Exec(`UPDATE sessions SET status = 'working' WHERE status IN ('running', 'waiting', 'error')`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE sessions SET status = 'idle' WHERE status = 'done'`)
	if err != nil {
		return err
	}

	return nil
}

func isAlterTableDuplicate(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
}
