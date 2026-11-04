// Package artifact stores single-file HTML snapshots published by sessions so
// they can be viewed (sandboxed) from the agmux UI.
package artifact

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/myuon/agmux/internal/db"
)

// MaxSize is the largest HTML file that can be published.
const MaxSize = 20 << 20

// FileName is the name every artifact is stored under inside its directory.
const FileName = "index.html"

var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ErrNotFound is returned when an artifact does not exist.
var ErrNotFound = errors.New("artifact not found")

// ValidID reports whether id has the exact shape of a generated artifact id.
// Everything that turns an id into a filesystem path must check this first.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// DefaultDir returns ~/.agmux/artifacts (honoring a swapped HOME).
func DefaultDir() (string, error) {
	base, err := db.AgmuxDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "artifacts"), nil
}

// Artifact is the metadata row of a published file.
type Artifact struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Title     string `json:"title"`
	CreatedAt string `json:"createdAt"`
	// UpdatedAt is the last time the content/title was replaced. It equals
	// CreatedAt for artifacts that were never updated.
	UpdatedAt string `json:"updatedAt"`
}

const selectCols = `id, session_id, title, created_at, COALESCE(NULLIF(updated_at, ''), created_at)`

func scanArtifact(sc interface{ Scan(...any) error }) (Artifact, error) {
	var a Artifact
	err := sc.Scan(&a.ID, &a.SessionID, &a.Title, &a.CreatedAt, &a.UpdatedAt)
	return a, err
}

// ValidateSource resolves path and checks that it is a publishable file.
// It returns the symlink-resolved path.
func ValidateSource(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("path must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path: %w", err)
	}
	ext := strings.ToLower(filepath.Ext(resolved))
	if ext != ".html" && ext != ".htm" {
		return "", errors.New("only .html or .htm files can be published")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("cannot stat file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("path is not a regular file")
	}
	if info.Size() > MaxSize {
		return "", fmt.Errorf("file is larger than %d MiB", MaxSize>>20)
	}
	return resolved, nil
}

// openSource validates src and opens it, re-checking on the handle so the
// file can't be swapped in between.
func openSource(src string) (*os.File, error) {
	resolved, err := ValidateSource(src)
	if err != nil {
		return nil, err
	}
	in, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	if info, err := in.Stat(); err != nil || !info.Mode().IsRegular() {
		in.Close()
		return nil, errors.New("path is not a regular file")
	}
	return in, nil
}

// copyLimited writes in to the new file at path (created exclusively, 0600),
// failing when it exceeds MaxSize. On error the file is removed.
func copyLimited(in io.Reader, path string) error {
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(in, MaxSize+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > MaxSize {
		err = fmt.Errorf("file is larger than %d MiB", MaxSize>>20)
	}
	if err != nil {
		_ = os.Remove(path)
	}
	return err
}

func randomHex() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

// Publish copies src into dir/<new random id>/index.html and returns the id.
func Publish(dir, src string) (string, error) {
	in, err := openSource(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	id, err := randomHex()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	itemDir := filepath.Join(dir, id)
	if err := os.Mkdir(itemDir, 0o700); err != nil {
		return "", err
	}
	if err := copyLimited(in, filepath.Join(itemDir, FileName)); err != nil {
		_ = os.RemoveAll(itemDir)
		return "", err
	}
	return id, nil
}

var (
	updateLocksMu sync.Mutex
	updateLocks   = map[string]*sync.Mutex{}
)

// LockUpdate serializes updates of one artifact within this process and
// returns the unlock function. Callers hold it across Replace, the metadata
// update and Commit/Rollback so concurrent updates can't interleave rollbacks.
func LockUpdate(id string) func() {
	updateLocksMu.Lock()
	m, ok := updateLocks[id]
	if !ok {
		m = &sync.Mutex{}
		updateLocks[id] = m
	}
	updateLocksMu.Unlock()
	m.Lock()
	return m.Unlock
}

// Replacement is a content swap that has been applied but can still be undone.
type Replacement struct {
	target, backup string
}

// Commit discards the backup of the previous content.
func (r *Replacement) Commit() { _ = os.Remove(r.backup) }

// Rollback restores the previous content.
func (r *Replacement) Rollback() error { return os.Rename(r.backup, r.target) }

// Replace atomically swaps the content of the existing artifact id with src.
// The new content is staged in a temp file inside the artifact directory and
// renamed into place, so readers see either all-old or all-new. The previous
// content is kept as a hard-linked backup until Commit/Rollback. If the
// artifact does not exist, or src is invalid, nothing is written.
func Replace(dir, id, src string) (*Replacement, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	itemDir := filepath.Join(dir, id)
	target := filepath.Join(itemDir, FileName)
	if info, err := os.Lstat(target); err != nil || !info.Mode().IsRegular() {
		return nil, ErrNotFound
	}
	in, err := openSource(src)
	if err != nil {
		return nil, err
	}
	defer in.Close()

	suffix, err := randomHex()
	if err != nil {
		return nil, err
	}
	tmp := filepath.Join(itemDir, ".tmp-"+suffix)
	backup := filepath.Join(itemDir, ".bak-"+suffix)
	if err := copyLimited(in, tmp); err != nil {
		return nil, err
	}
	if err := os.Link(target, backup); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		_ = os.Remove(backup)
		return nil, err
	}
	return &Replacement{target: target, backup: backup}, nil
}

// Store keeps artifact metadata in sqlite and files under dir.
type Store struct {
	db  *sql.DB
	dir string
}

func NewStore(sqlDB *sql.DB, dir string) *Store { return &Store{db: sqlDB, dir: dir} }

// Dir is the artifacts root directory.
func (s *Store) Dir() string { return s.dir }

// FilePath returns the stored file path of a valid id.
func (s *Store) FilePath(id string) (string, error) {
	if !ValidID(id) {
		return "", ErrNotFound
	}
	return filepath.Join(s.dir, id, FileName), nil
}

// Register records metadata for a file already copied by Publish.
func (s *Store) Register(sessionID, id, title string) (*Artifact, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("title is required")
	}
	p, err := s.FilePath(id)
	if err != nil {
		return nil, err
	}
	if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() {
		return nil, ErrNotFound
	}
	now := time.Now().UTC().Format(time.RFC3339)
	a := &Artifact{ID: id, SessionID: sessionID, Title: title, CreatedAt: now, UpdatedAt: now}
	_, err = s.db.Exec(`INSERT INTO artifacts (id, session_id, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		a.ID, a.SessionID, a.Title, a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) Get(id string) (*Artifact, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	a, err := scanArtifact(s.db.QueryRow(`SELECT `+selectCols+` FROM artifacts WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &a, err
}

// Touch records an update of an existing artifact: updated_at moves to now
// (nanosecond precision so quick successive updates differ) and the title is
// replaced when non-empty. session_id and created_at are left alone.
func (s *Store) Touch(id, title string) (*Artifact, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	title = strings.TrimSpace(title)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	res, err := s.db.Exec(`UPDATE artifacts SET updated_at = ?, title = COALESCE(NULLIF(?, ''), title) WHERE id = ?`, now, title, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.Get(id)
}

// List returns a session's artifacts, newest first.
func (s *Store) List(sessionID string) ([]Artifact, error) {
	rows, err := s.db.Query(`SELECT `+selectCols+` FROM artifacts WHERE session_id = ? ORDER BY created_at DESC, rowid DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Artifact{}
	for rows.Next() {
		a, err := scanArtifact(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Delete removes the row and the artifact directory. It only ever removes
// dir/<validated id>.
func (s *Store) Delete(id string) error {
	if _, err := s.Get(id); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM artifacts WHERE id = ?`, id); err != nil {
		return err
	}
	return RemoveFiles(s.dir, id)
}

// RemoveFiles deletes dir/<id> after strictly validating id.
func RemoveFiles(dir, id string) error {
	if !ValidID(id) {
		return ErrNotFound
	}
	target := filepath.Join(dir, id)
	if filepath.Dir(target) != filepath.Clean(dir) {
		return ErrNotFound
	}
	return os.RemoveAll(target)
}
