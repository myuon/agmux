package secret

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// MaxFileValueLength bounds values stored by the file backend.
const MaxFileValueLength = 1 << 20

// FileBackend stores one file per secret in Dir (mode 0700), each file
// mode 0600. It is the fallback when the Keychain is not usable.
type FileBackend struct {
	Dir string
}

// NewFileBackend returns a FileBackend rooted at dir.
func NewFileBackend(dir string) *FileBackend {
	return &FileBackend{Dir: dir}
}

func (b *FileBackend) Kind() BackendKind { return BackendFile }

func (b *FileBackend) path(name string) (string, error) {
	if err := ValidateName(name); err != nil {
		return "", err
	}
	return filepath.Join(b.Dir, name), nil
}

func (b *FileBackend) ensureDir() error {
	if err := os.MkdirAll(b.Dir, 0o700); err != nil {
		return fmt.Errorf("create secrets dir: %w", err)
	}
	// MkdirAll does not fix permissions of an existing dir.
	if err := os.Chmod(b.Dir, 0o700); err != nil {
		return fmt.Errorf("chmod secrets dir: %w", err)
	}
	return nil
}

func (b *FileBackend) Set(_ context.Context, name, value string) error {
	p, err := b.path(name)
	if err != nil {
		return err
	}
	if len(value) > MaxFileValueLength {
		return fmt.Errorf("secret value is too long (max %d bytes)", MaxFileValueLength)
	}
	if err := b.ensureDir(); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(b.Dir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if _, err := tmp.WriteString(value); err != nil {
		tmp.Close()
		return errors.New("write secret file failed")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("write secret file failed")
	}
	if err := os.Rename(tmpName, p); err != nil {
		return fmt.Errorf("store secret file: %w", err)
	}
	return nil
}

func (b *FileBackend) Get(_ context.Context, s *Secret) (string, error) {
	p, err := b.path(s.Name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read secret file: %w", err)
	}
	return string(data), nil
}

func (b *FileBackend) Delete(_ context.Context, name string) error {
	p, err := b.path(name)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return err
}
