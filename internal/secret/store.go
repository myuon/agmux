package secret

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Store persists secret METADATA in the agmux DB (table `secrets`, created by
// db.Open's migrations). It never sees secret values.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore returns a Store backed by db.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now}
}

const selectCols = `name, description, backend, ref, allowed_projects, created_at, updated_at, last_used_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanSecret(sc scanner) (*Secret, error) {
	var (
		s                    Secret
		backend, allowedJSON string
		createdAt, updatedAt string
		lastUsedAt           sql.NullString
	)
	if err := sc.Scan(&s.Name, &s.Description, &backend, &s.Ref, &allowedJSON, &createdAt, &updatedAt, &lastUsedAt); err != nil {
		return nil, err
	}
	s.Backend = BackendKind(backend)
	if allowedJSON != "" {
		if err := json.Unmarshal([]byte(allowedJSON), &s.AllowedProjects); err != nil {
			return nil, fmt.Errorf("decode allowed_projects of %s: %w", s.Name, err)
		}
	}
	if s.AllowedProjects == nil {
		s.AllowedProjects = []string{}
	}
	s.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	s.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if lastUsedAt.Valid && lastUsedAt.String != "" {
		if t, err := time.Parse(time.RFC3339Nano, lastUsedAt.String); err == nil {
			s.LastUsedAt = &t
		}
	}
	return &s, nil
}

// List returns all secrets ordered by name.
func (st *Store) List() ([]Secret, error) {
	rows, err := st.db.Query(`SELECT ` + selectCols + ` FROM secrets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Secret{}
	for rows.Next() {
		s, err := scanSecret(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// Get returns the secret named name, or ErrNotFound.
func (st *Store) Get(name string) (*Secret, error) {
	s, err := scanSecret(st.db.QueryRow(`SELECT `+selectCols+` FROM secrets WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return s, err
}

// Upsert inserts or updates the metadata of s (keyed by name). created_at
// and last_used_at are preserved on update. It reports whether a new row was
// created.
func (st *Store) Upsert(s Secret) (*Secret, bool, error) {
	if err := ValidateName(s.Name); err != nil {
		return nil, false, err
	}
	allowed := s.AllowedProjects
	if allowed == nil {
		allowed = []string{}
	}
	allowedJSON, err := json.Marshal(allowed)
	if err != nil {
		return nil, false, err
	}
	now := st.now().UTC().Format(time.RFC3339Nano)

	_, getErr := st.Get(s.Name)
	created := errors.Is(getErr, ErrNotFound)
	if getErr != nil && !created {
		return nil, false, getErr
	}
	if created {
		_, err = st.db.Exec(`INSERT INTO secrets (name, description, backend, ref, allowed_projects, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, s.Name, s.Description, string(s.Backend), s.Ref, string(allowedJSON), now, now)
	} else {
		_, err = st.db.Exec(`UPDATE secrets SET description = ?, backend = ?, ref = ?, allowed_projects = ?, updated_at = ?
			WHERE name = ?`, s.Description, string(s.Backend), s.Ref, string(allowedJSON), now, s.Name)
	}
	if err != nil {
		return nil, false, err
	}
	got, err := st.Get(s.Name)
	return got, created, err
}

// Delete removes the metadata row. Returns ErrNotFound if absent.
func (st *Store) Delete(name string) error {
	res, err := st.db.Exec(`DELETE FROM secrets WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkUsed records that the secret was used now.
func (st *Store) MarkUsed(name string) error {
	return st.markUsedAt(name, st.now())
}

func (st *Store) markUsedAt(name string, t time.Time) error {
	res, err := st.db.Exec(`UPDATE secrets SET last_used_at = ? WHERE name = ?`,
		t.UTC().Format(time.RFC3339Nano), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
