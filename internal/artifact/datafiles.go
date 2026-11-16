package artifact

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/myuon/agmux/internal/db"
)

// Data files let an artifact fetch whitelisted files that are read fresh from
// disk on every request (GET /artifacts/{id}/data/{name}). Each registered
// path must resolve to a regular file inside the publishing session's project
// directory and never inside the agmux data dir (~/.agmux), because anyone
// who can view artifacts over the network can read them.

// MaxDataFiles is the largest number of data files one artifact can register.
const MaxDataFiles = 16

// MaxDataSize is the largest data file that is served.
const MaxDataSize = 5 << 20

const maxDataNameLen = 128

var dataNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-][A-Za-z0-9._-]*$`)

// ErrDataTooLarge is returned by OpenDataFile when the file exceeds MaxDataSize.
var ErrDataTooLarge = errors.New("data file is too large")

// ValidDataName reports whether name can be used as a data file name: only
// [A-Za-z0-9._-], no leading dot (so never "." or ".."), no slash.
func ValidDataName(name string) bool {
	return len(name) <= maxDataNameLen && dataNamePattern.MatchString(name)
}

// DataFiles is the validated data file mapping of an artifact.
type DataFiles struct {
	// Root is the symlink-resolved project directory every path must stay in.
	Root string
	// Paths maps name → symlink-resolved absolute path.
	Paths map[string]string
}

// Names returns the registered names, sorted.
func (d *DataFiles) Names() []string {
	if d == nil {
		return nil
	}
	out := make([]string, 0, len(d.Paths))
	for n := range d.Paths {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// resolveDir returns the absolute, symlink-resolved form of dir.
func resolveDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// within reports whether path is root itself or below it. Both must be clean
// absolute paths.
func within(path, root string) bool {
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// deniedRoots are directories data files may never come from: the agmux data
// dir (secrets, DB, config, artifacts) plus any extra dirs given by the caller.
func deniedRoots(extra []string) []string {
	var out []string
	add := func(d string) {
		if d == "" {
			return
		}
		if r, err := resolveDir(d); err == nil {
			out = append(out, r)
		} else if abs, err := filepath.Abs(d); err == nil {
			out = append(out, filepath.Clean(abs))
		}
	}
	if d, err := db.AgmuxDir(); err == nil {
		add(d)
	}
	for _, d := range extra {
		add(d)
	}
	return out
}

// checkDataPath resolves p and verifies that it is a regular file inside root
// and outside every denied dir. It returns the resolved path.
func checkDataPath(p, root string, denied []string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", errors.New("path must be absolute")
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", fmt.Errorf("cannot resolve path: %w", err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	if !within(resolved, root) {
		return "", fmt.Errorf("path must be inside the session's project directory %s", root)
	}
	for _, d := range denied {
		if within(resolved, d) {
			return "", errors.New("files under the agmux data directory cannot be registered")
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("cannot stat file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("path is not a regular file")
	}
	return resolved, nil
}

// ResolveDataFiles validates a name → absolute path mapping for an artifact
// published by a session whose project directory is projectDir. extraDenied
// are additional directories that must never be served (e.g. the artifacts
// dir). An empty mapping yields an empty (non-nil) DataFiles.
func ResolveDataFiles(files map[string]string, projectDir string, extraDenied ...string) (*DataFiles, error) {
	if len(files) == 0 {
		return &DataFiles{Paths: map[string]string{}}, nil
	}
	if len(files) > MaxDataFiles {
		return nil, fmt.Errorf("data_files: at most %d entries are allowed", MaxDataFiles)
	}
	if strings.TrimSpace(projectDir) == "" {
		return nil, errors.New("data_files: the publishing session has no project directory")
	}
	root, err := resolveDir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("data_files: cannot resolve project directory: %w", err)
	}
	denied := deniedRoots(extraDenied)
	for _, d := range denied {
		if within(root, d) {
			return nil, errors.New("data_files: the project directory is inside the agmux data directory")
		}
	}
	out := &DataFiles{Root: root, Paths: make(map[string]string, len(files))}
	for name, p := range files {
		if !ValidDataName(name) {
			return nil, fmt.Errorf("data_files: invalid name %q (use [A-Za-z0-9._-], no leading dot, max %d chars)", name, maxDataNameLen)
		}
		resolved, err := checkDataPath(p, root, denied)
		if err != nil {
			return nil, fmt.Errorf("data_files[%q]: %v", name, err)
		}
		out.Paths[name] = resolved
	}
	return out, nil
}

// ReadDataFile re-validates a stored path at request time (the file may have
// been replaced by a symlink pointing elsewhere since it was registered) and
// returns its current content. It fails with ErrDataTooLarge when the file is
// larger than MaxDataSize, and with another error when it is missing or no
// longer allowed.
func ReadDataFile(root, path string, extraDenied ...string) ([]byte, error) {
	if root == "" || !filepath.IsAbs(root) {
		return nil, ErrNotFound
	}
	resolved, err := checkDataPath(path, filepath.Clean(root), deniedRoots(extraDenied))
	if err != nil {
		return nil, err
	}
	// O_NOFOLLOW: refuse if the final component was swapped for a symlink
	// after it was resolved.
	f, err := os.OpenFile(resolved, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if info.Size() > MaxDataSize {
		return nil, ErrDataTooLarge
	}
	b, err := io.ReadAll(io.LimitReader(f, MaxDataSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxDataSize {
		return nil, ErrDataTooLarge
	}
	return b, nil
}

func encodeDataFiles(d *DataFiles) (paths, root string) {
	if d == nil || len(d.Paths) == 0 {
		return "", ""
	}
	b, _ := json.Marshal(d.Paths)
	return string(b), d.Root
}

func decodeDataFiles(paths, root string) *DataFiles {
	if paths == "" {
		return nil
	}
	m := map[string]string{}
	if err := json.Unmarshal([]byte(paths), &m); err != nil || len(m) == 0 {
		return nil
	}
	return &DataFiles{Root: root, Paths: m}
}
