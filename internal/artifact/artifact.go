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

// Publish copies src into dir/<new random id>/index.html and returns the id.
func Publish(dir, src string) (string, error) {
	resolved, err := ValidateSource(src)
	if err != nil {
		return "", err
	}
	in, err := os.Open(resolved)
	if err != nil {
		return "", err
	}
	defer in.Close()
	// Re-check on the opened handle so the file can't be swapped in between.
	if info, err := in.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", errors.New("path is not a regular file")
	}

	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(raw[:])

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	itemDir := filepath.Join(dir, id)
	if err := os.Mkdir(itemDir, 0o700); err != nil {
		return "", err
	}
	out, err := os.OpenFile(filepath.Join(itemDir, FileName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = os.RemoveAll(itemDir)
		return "", err
	}
	n, err := io.Copy(out, io.LimitReader(in, MaxSize+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > MaxSize {
		err = fmt.Errorf("file is larger than %d MiB", MaxSize>>20)
	}
	if err != nil {
		_ = os.RemoveAll(itemDir)
		return "", err
	}
	return id, nil
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
	a := &Artifact{ID: id, SessionID: sessionID, Title: title, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	_, err = s.db.Exec(`INSERT INTO artifacts (id, session_id, title, created_at) VALUES (?, ?, ?, ?)`,
		a.ID, a.SessionID, a.Title, a.CreatedAt)
	if err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) Get(id string) (*Artifact, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	var a Artifact
	err := s.db.QueryRow(`SELECT id, session_id, title, created_at FROM artifacts WHERE id = ?`, id).
		Scan(&a.ID, &a.SessionID, &a.Title, &a.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &a, err
}

// List returns a session's artifacts, newest first.
func (s *Store) List(sessionID string) ([]Artifact, error) {
	rows, err := s.db.Query(`SELECT id, session_id, title, created_at FROM artifacts WHERE session_id = ? ORDER BY created_at DESC, rowid DESC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Artifact{}
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.SessionID, &a.Title, &a.CreatedAt); err != nil {
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
