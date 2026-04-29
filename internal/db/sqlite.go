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

// DBPathForPort returns the DB file path based on the port number.
// For the default port (4321), it returns ~/.agmux/agmux.db (backward compatible).
// For other ports, it returns ~/.agmux/agmux-<port>.db.
func DBPathForPort(port int) (string, error) {
	dir, err := AgmuxDir()
	if err != nil {
		return "", err
	}
	if port == 4321 {
		return filepath.Join(dir, "agmux.db"), nil
	}
	return filepath.Join(dir, fmt.Sprintf("agmux-%d.db", port)), nil
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
			tmux_session TEXT NOT NULL DEFAULT '',
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
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN output_mode TEXT NOT NULL DEFAULT 'stream'`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: update all terminal sessions to stream (terminal mode removed)
	_, _ = db.Exec(`UPDATE sessions SET output_mode = 'stream' WHERE output_mode = 'terminal'`)

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

	// Migration: add goals (JSON array) column if missing
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN goals TEXT NOT NULL DEFAULT '[]'`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: add provider column if missing
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN provider TEXT NOT NULL DEFAULT 'claude'`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: add cli_session_id column if missing (for Codex session resume)
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN cli_session_id TEXT NOT NULL DEFAULT ''`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: add model column if missing (for model selection)
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN model TEXT NOT NULL DEFAULT ''`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: add last_error column if missing (for error display)
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN last_error TEXT`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: add system_prompt column if missing (for per-session system prompt)
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN system_prompt TEXT`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: add parent_session_id column if missing (for fork tracking)
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN parent_session_id TEXT`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: add holder_pid column if missing (for holder process tracking)
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN holder_pid INTEGER NOT NULL DEFAULT 0`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: add clear_offset column if missing (byte offset for JSONL clear point)
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN clear_offset INTEGER NOT NULL DEFAULT 0`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: remove UNIQUE constraint from tmux_session (no longer used)
	// SQLite doesn't support DROP CONSTRAINT, so we recreate the table
	{
		var hasUnique bool
		row := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='sessions'`)
		var createSQL string
		if err := row.Scan(&createSQL); err == nil {
			hasUnique = strings.Contains(createSQL, "tmux_session") && strings.Contains(createSQL, "UNIQUE")
		}
		if hasUnique {
			_, err = db.Exec(`
				CREATE TABLE sessions_new (
					id TEXT PRIMARY KEY,
					name TEXT NOT NULL,
					project_path TEXT NOT NULL,
					initial_prompt TEXT,
					tmux_session TEXT NOT NULL DEFAULT '',
					status TEXT NOT NULL DEFAULT 'working',
					type TEXT NOT NULL DEFAULT 'worker',
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					output_mode TEXT NOT NULL DEFAULT 'stream',
					current_task TEXT,
					goal TEXT,
					goals TEXT NOT NULL DEFAULT '[]',
					provider TEXT NOT NULL DEFAULT 'claude',
					cli_session_id TEXT NOT NULL DEFAULT '',
					model TEXT NOT NULL DEFAULT ''
				);
				INSERT INTO sessions_new SELECT id, name, project_path, initial_prompt, tmux_session, status, type, created_at, updated_at, output_mode, current_task, goal, goals, provider, cli_session_id, model FROM sessions;
				DROP TABLE sessions;
				ALTER TABLE sessions_new RENAME TO sessions;
			`)
			if err != nil {
				return fmt.Errorf("migrate tmux_session unique: %w", err)
			}
		}
	}

	// Migration: create otel_metrics table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS otel_metrics (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			value REAL NOT NULL,
			attributes TEXT NOT NULL DEFAULT '{}',
			resource_attributes TEXT NOT NULL DEFAULT '{}',
			session_id TEXT,
			timestamp DATETIME NOT NULL,
			received_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Migration: create otel_events table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS otel_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			body TEXT NOT NULL DEFAULT '{}',
			attributes TEXT NOT NULL DEFAULT '{}',
			resource_attributes TEXT NOT NULL DEFAULT '{}',
			session_id TEXT,
			timestamp DATETIME NOT NULL,
			received_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}

	// Migration: create indexes for otel tables
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_otel_metrics_name ON otel_metrics(name)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_otel_metrics_session ON otel_metrics(session_id)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_otel_metrics_timestamp ON otel_metrics(timestamp)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_otel_events_name ON otel_events(name)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_otel_events_session ON otel_events(session_id)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_otel_events_timestamp ON otel_events(timestamp)`)

	// Migration: backfill session_id from event attributes where missing
	_, _ = db.Exec(`
		UPDATE otel_events
		SET session_id = json_extract(attributes, '$."session.id"')
		WHERE (session_id IS NULL OR session_id = '')
		  AND json_extract(attributes, '$."session.id"') IS NOT NULL
		  AND json_extract(attributes, '$."session.id"') != ''
	`)

	// Migration: update old status values to new ones
	_, err = db.Exec(`UPDATE sessions SET status = 'working' WHERE status IN ('running', 'waiting', 'error')`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE sessions SET status = 'idle' WHERE status = 'done'`)
	if err != nil {
		return err
	}

	// Migration: convert deprecated status values to new ones
	_, err = db.Exec(`UPDATE sessions SET status = 'paused' WHERE status = 'stopped'`)
	if err != nil {
		return err
	}
	_, err = db.Exec(`UPDATE sessions SET status = 'waiting_input' WHERE status IN ('question_waiting', 'alignment_needed')`)
	if err != nil {
		return err
	}

	// Migration: add role_template column if missing (for tracking which template was used)
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN role_template TEXT`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: create notifications table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			message TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_notifications_session ON notifications(session_id)`)
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_notifications_created ON notifications(created_at)`)

	// Migration: add conversation_started column if missing
	// This tracks whether at least one full conversation turn has been completed.
	// When false, auto-recovery must not use --resume (conversation doesn't exist in Claude yet).
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN conversation_started INTEGER NOT NULL DEFAULT 0`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}
	// Backfill: sessions with a non-empty cli_session_id and initial_prompt are assumed to have started.
	_, _ = db.Exec(`UPDATE sessions SET conversation_started = 1 WHERE cli_session_id != '' AND initial_prompt != '' AND initial_prompt IS NOT NULL`)

	// Migration: add ephemeral_timeout_seconds column if missing
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN ephemeral_timeout_seconds INTEGER`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: add completion_report column if missing
	_, err = db.Exec(`ALTER TABLE sessions ADD COLUMN completion_report TEXT`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	// Migration: create background_tasks table
	// Tracks long-running tasks (agent / tool background tasks) detected from JSONL stream.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS background_tasks (
			session_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			task_type TEXT NOT NULL DEFAULT 'unknown',
			agent_id TEXT,
			description TEXT,
			started_at TEXT,
			last_tool_name TEXT,
			last_tool_input TEXT,
			output TEXT,
			usage_input_tokens INTEGER,
			usage_output_tokens INTEGER,
			tool_call_history TEXT NOT NULL DEFAULT '[]',
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (session_id, task_id)
		)
	`)
	if err != nil {
		return err
	}
	_, _ = db.Exec(`CREATE INDEX IF NOT EXISTS idx_background_tasks_session ON background_tasks(session_id)`)

	// Migration: add dismissed_at column for logical delete.
	// Physical delete + UPSERT could resurrect a dismissed task when a delayed
	// task_progress event arrived. With dismissed_at set, the row stays in the
	// DB as a tombstone and List/UPSERT skip it.
	_, err = db.Exec(`ALTER TABLE background_tasks ADD COLUMN dismissed_at DATETIME`)
	if err != nil && !isAlterTableDuplicate(err) {
		return err
	}

	return nil
}

func isAlterTableDuplicate(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate column name")
}
