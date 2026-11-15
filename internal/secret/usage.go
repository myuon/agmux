package secret

import (
	"database/sql"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// UsageStatus is the outcome of a secret use attempt.
type UsageStatus string

const (
	// UsageUsed means the secret was injected into a child process.
	UsageUsed UsageStatus = "used"
	// UsageDenied means the project scope check refused the secret.
	UsageDenied UsageStatus = "denied"
)

// usageTimeFormat is fixed-width (UTC) so used_at sorts lexicographically.
const usageTimeFormat = "2006-01-02T15:04:05.000000000Z07:00"

// maxCommandLength bounds the stored command name.
const maxCommandLength = 128

// Usage is one audit log entry of `agmux secret exec`. It deliberately keeps
// only the command's basename and argument count: arguments may contain
// secrets and are never recorded.
type Usage struct {
	ID          int64       `json:"id"`
	SecretName  string      `json:"secretName"`
	SessionID   string      `json:"sessionId"`
	ProjectPath string      `json:"projectPath"`
	Cwd         string      `json:"cwd"`
	Command     string      `json:"command"`
	ArgCount    int         `json:"argCount"`
	ExitCode    *int        `json:"exitCode,omitempty"`
	Status      UsageStatus `json:"status"`
	UsedAt      time.Time   `json:"usedAt"`
}

// CommandName reduces argv[0] to its basename, bounded in length. Callers
// must never pass anything other than argv[0].
func CommandName(argv0 string) string {
	argv0 = strings.TrimSpace(argv0)
	if argv0 == "" {
		return ""
	}
	name := filepath.Base(argv0)
	if len(name) > maxCommandLength {
		name = name[:maxCommandLength]
		for !utf8.ValidString(name) {
			name = name[:len(name)-1]
		}
	}
	return name
}

// RecordUsage appends u to the audit log. Command is normalized with
// CommandName. A "used" entry also updates the secret's last_used_at and
// returns ErrNotFound if the secret does not exist; a "denied" entry is
// recorded regardless.
func (st *Store) RecordUsage(u Usage) (*Usage, error) {
	if u.Status == "" {
		u.Status = UsageUsed
	}
	if u.ArgCount < 0 {
		u.ArgCount = 0
	}
	u.Command = CommandName(u.Command)
	if u.Status != UsageUsed {
		u.ExitCode = nil
	}
	if u.UsedAt.IsZero() {
		u.UsedAt = st.now()
	}
	u.UsedAt = u.UsedAt.UTC()

	if u.Status == UsageUsed {
		if err := st.markUsedAt(u.SecretName, u.UsedAt); err != nil {
			return nil, err
		}
	}
	var exit sql.NullInt64
	if u.ExitCode != nil {
		exit = sql.NullInt64{Int64: int64(*u.ExitCode), Valid: true}
	}
	res, err := st.db.Exec(`INSERT INTO secret_usages
		(secret_name, session_id, project_path, cwd, command, arg_count, exit_code, status, used_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.SecretName, u.SessionID, u.ProjectPath, u.Cwd, u.Command, u.ArgCount, exit, string(u.Status),
		u.UsedAt.Format(usageTimeFormat))
	if err != nil {
		return nil, err
	}
	u.ID, _ = res.LastInsertId()
	return &u, nil
}

// DefaultUsageLimit and MaxUsageLimit bound ListUsages.
const (
	DefaultUsageLimit = 50
	MaxUsageLimit     = 500
)

// ListUsages returns the newest audit entries first. name filters by secret
// name when non-empty. limit <= 0 uses DefaultUsageLimit; it is capped at
// MaxUsageLimit.
func (st *Store) ListUsages(name string, limit int) ([]Usage, error) {
	if limit <= 0 {
		limit = DefaultUsageLimit
	}
	if limit > MaxUsageLimit {
		limit = MaxUsageLimit
	}
	q := `SELECT id, secret_name, session_id, project_path, cwd, command, arg_count, exit_code, status, used_at
		FROM secret_usages`
	args := []any{}
	if name != "" {
		q += ` WHERE secret_name = ?`
		args = append(args, name)
	}
	q += ` ORDER BY used_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := st.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Usage{}
	for rows.Next() {
		var (
			u      Usage
			exit   sql.NullInt64
			status string
			usedAt string
		)
		if err := rows.Scan(&u.ID, &u.SecretName, &u.SessionID, &u.ProjectPath, &u.Cwd, &u.Command, &u.ArgCount, &exit, &status, &usedAt); err != nil {
			return nil, err
		}
		if exit.Valid {
			c := int(exit.Int64)
			u.ExitCode = &c
		}
		u.Status = UsageStatus(status)
		u.UsedAt, _ = time.Parse(time.RFC3339Nano, usedAt)
		out = append(out, u)
	}
	return out, rows.Err()
}
