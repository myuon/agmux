package learning

import (
	"database/sql"
	"fmt"
	"time"
)

// Target kinds a proposal can write to.
const (
	TargetClaudeMD = "claude_md"
	TargetSkill    = "skill"
	TargetMemory   = "memory"
)

// Proposal statuses.
const (
	StatusPending  = "pending"
	StatusApplying = "applying" // accepted; a session is turning it into a PR
	StatusApplied  = "applied"
	StatusRejected = "rejected"
	StatusFailed   = "failed"
)

// Proposal is a suggested addition to CLAUDE.md, a skill or memory.
type Proposal struct {
	ID             int64      `json:"id"`
	SessionID      string     `json:"sessionId"`
	SessionName    string     `json:"sessionName"`
	ProjectPath    string     `json:"projectPath"`
	Target         string     `json:"target"`
	Name           string     `json:"name"` // skill name or memory slug; empty for claude_md
	MemoryType     string     `json:"memoryType,omitempty"`
	Title          string     `json:"title"`
	Content        string     `json:"content"`
	Reason         string     `json:"reason"`
	Status         string     `json:"status"`
	ApplySessionID string     `json:"applySessionId,omitempty"`
	Error          string     `json:"error,omitempty"`
	CreatedAt      time.Time  `json:"createdAt"`
	DecidedAt      *time.Time `json:"decidedAt,omitempty"`
}

// Store persists proposals and per-session review progress.
type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) (*Store, error) {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS learning_proposals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			session_name TEXT NOT NULL DEFAULT '',
			project_path TEXT NOT NULL,
			target TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			memory_type TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			apply_session_id TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			decided_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_learning_proposals_status ON learning_proposals(status);
		CREATE TABLE IF NOT EXISTS learning_reviews (
			session_id TEXT PRIMARY KEY,
			offset INTEGER NOT NULL,
			reviewed_at DATETIME NOT NULL
		);
	`)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

const proposalCols = `id, session_id, session_name, project_path, target, name, memory_type, title, content, reason, status, apply_session_id, error, created_at, decided_at`

func scanProposal(sc interface{ Scan(...interface{}) error }) (*Proposal, error) {
	var p Proposal
	var decided sql.NullTime
	if err := sc.Scan(&p.ID, &p.SessionID, &p.SessionName, &p.ProjectPath, &p.Target, &p.Name, &p.MemoryType, &p.Title, &p.Content, &p.Reason, &p.Status, &p.ApplySessionID, &p.Error, &p.CreatedAt, &decided); err != nil {
		return nil, err
	}
	if decided.Valid {
		t := decided.Time
		p.DecidedAt = &t
	}
	return &p, nil
}

func (s *Store) Insert(p *Proposal) error {
	p.Status = StatusPending
	p.CreatedAt = time.Now()
	res, err := s.db.Exec(`INSERT INTO learning_proposals (session_id, session_name, project_path, target, name, memory_type, title, content, reason, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.SessionID, p.SessionName, p.ProjectPath, p.Target, p.Name, p.MemoryType, p.Title, p.Content, p.Reason, p.Status, p.CreatedAt)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) Get(id int64) (*Proposal, error) {
	row := s.db.QueryRow(`SELECT `+proposalCols+` FROM learning_proposals WHERE id = ?`, id)
	p, err := scanProposal(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("proposal not found: %d", id)
	}
	return p, err
}

// List returns proposals, newest first. Empty status means all.
func (s *Store) List(status string, limit int) ([]*Proposal, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT ` + proposalCols + ` FROM learning_proposals`
	args := []interface{}{}
	if status != "" {
		q += ` WHERE status = ?`
		args = append(args, status)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Proposal{}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Rejected returns recently rejected proposals for a project, used to keep
// the reviewer from suggesting them again.
func (s *Store) Rejected(projectPath string, limit int) ([]*Proposal, error) {
	rows, err := s.db.Query(`SELECT `+proposalCols+` FROM learning_proposals WHERE project_path = ? AND status = ? ORDER BY id DESC LIMIT ?`, projectPath, StatusRejected, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Proposal{}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Pending returns pending proposals for a project (to avoid duplicates).
func (s *Store) Pending(projectPath string) ([]*Proposal, error) {
	rows, err := s.db.Query(`SELECT `+proposalCols+` FROM learning_proposals WHERE project_path = ? AND status = ? ORDER BY id DESC LIMIT 50`, projectPath, StatusPending)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Proposal{}
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Decide moves a pending proposal to a new status. It fails if the proposal
// is no longer pending, so a proposal can be decided only once.
func (s *Store) Decide(id int64, status, content, applySessionID, errMsg string) error {
	res, err := s.db.Exec(`UPDATE learning_proposals SET status = ?, content = ?, apply_session_id = ?, error = ?, decided_at = ? WHERE id = ? AND status = ?`,
		status, content, applySessionID, errMsg, time.Now(), id, StatusPending)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("proposal %d is not pending", id)
	}
	return nil
}

func (s *Store) ReviewOffset(sessionID string) (int64, error) {
	var off int64
	err := s.db.QueryRow(`SELECT offset FROM learning_reviews WHERE session_id = ?`, sessionID).Scan(&off)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return off, err
}

func (s *Store) SetReviewOffset(sessionID string, off int64) error {
	_, err := s.db.Exec(`INSERT INTO learning_reviews (session_id, offset, reviewed_at) VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET offset = excluded.offset, reviewed_at = excluded.reviewed_at`, sessionID, off, time.Now())
	return err
}
