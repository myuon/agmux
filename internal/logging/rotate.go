package logging

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// DefaultMaxSize is the size at which agmux.log is rotated.
	DefaultMaxSize int64 = 100 << 20

	rotatedTimeFormat = "20060102-150405"
)

// RotatingFile is an io.WriteCloser that writes to <dir>/agmux.log and
// rotates it when the local date changes or the size limit would be exceeded.
// Rotated files are renamed to agmux-YYYYMMDD-HHMMSS.log, gzipped in the
// background, and deleted once older than the retention days.
type RotatingFile struct {
	mu      sync.Mutex
	dir     string
	path    string
	file    *os.File
	size    int64
	day     string // local date (YYYYMMDD) the current file was opened for
	maxSize int64
	days    int // retention days for rotated logs; <= 0 keeps them forever
	now     func() time.Time
	// redirect, when set, makes every (re)opened file also become fd 1 and 2
	// (launchd points stdout/stderr at agmux.log, so panics must follow it).
	redirect bool

	maintMu sync.Mutex // serializes gzip/prune
	wg      sync.WaitGroup
}

type rotateOptions struct {
	maxSize  int64
	days     int
	now      func() time.Time
	redirect bool
}

func openRotating(dir string, o rotateOptions) (*RotatingFile, error) {
	if o.maxSize <= 0 {
		o.maxSize = DefaultMaxSize
	}
	if o.now == nil {
		o.now = time.Now
	}
	r := &RotatingFile{
		dir:      dir,
		path:     filepath.Join(dir, "agmux.log"),
		maxSize:  o.maxSize,
		days:     o.days,
		now:      o.now,
		redirect: o.redirect,
	}
	// A leftover log from a previous day is rotated before anything is written.
	if fi, err := os.Stat(r.path); err == nil && fi.Size() > 0 && dayKey(fi.ModTime()) != dayKey(r.now()) {
		if err := r.renameCurrent(); err != nil {
			return nil, err
		}
	}
	if err := r.open(); err != nil {
		return nil, err
	}
	r.startMaintenance()
	return r, nil
}

func dayKey(t time.Time) string { return t.Local().Format("20060102") }

// open opens agmux.log for appending. Caller holds mu (or is the constructor).
func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.file = f
	r.size = fi.Size()
	r.day = dayKey(r.now())
	if r.redirect {
		redirectStdio(f)
	}
	return nil
}

// renameCurrent renames agmux.log to a timestamped name.
func (r *RotatingFile) renameCurrent() error {
	base := "agmux-" + r.now().Local().Format(rotatedTimeFormat)
	dst := filepath.Join(r.dir, base+".log")
	for i := 1; ; i++ {
		_, e1 := os.Stat(dst)
		_, e2 := os.Stat(dst + ".gz")
		if os.IsNotExist(e1) && os.IsNotExist(e2) {
			break
		}
		dst = filepath.Join(r.dir, fmt.Sprintf("%s-%d.log", base, i))
	}
	return os.Rename(r.path, dst)
}

func (r *RotatingFile) rotate() error {
	// Rename, open the new file, then close the old one so there is always
	// a valid destination for writers.
	old := r.file
	if err := r.renameCurrent(); err != nil {
		return err
	}
	if err := r.open(); err != nil {
		r.file = old
		return err
	}
	old.Close()
	r.startMaintenance()
	return nil
}

func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return 0, os.ErrClosed
	}
	if dayKey(r.now()) != r.day || (r.size > 0 && r.size+int64(len(p)) > r.maxSize) {
		// On failure keep writing to the current file.
		_ = r.rotate()
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

// Close closes the log file and waits for background maintenance.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	var err error
	if r.file != nil {
		err = r.file.Close()
		r.file = nil
	}
	r.mu.Unlock()
	r.wg.Wait()
	return err
}

func (r *RotatingFile) startMaintenance() {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.maintain()
	}()
}

// maintain gzips rotated logs and removes expired ones.
func (r *RotatingFile) maintain() {
	r.maintMu.Lock()
	defer r.maintMu.Unlock()
	plain, _ := filepath.Glob(filepath.Join(r.dir, "agmux-*.log"))
	for _, p := range plain {
		_ = gzipFile(p)
	}
	if r.days <= 0 {
		return
	}
	cutoff := r.now().AddDate(0, 0, -r.days)
	for _, pat := range []string{"agmux-*.log", "agmux-*.log.gz"} {
		matches, _ := filepath.Glob(filepath.Join(r.dir, pat))
		for _, p := range matches {
			if fi, err := os.Stat(p); err == nil && fi.ModTime().Before(cutoff) {
				_ = os.Remove(p)
			}
		}
	}
}

func gzipFile(path string) (err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := path + ".gz.tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			out.Close()
			os.Remove(tmp)
		}
	}()
	zw := gzip.NewWriter(out)
	if _, err = io.Copy(zw, in); err != nil {
		return err
	}
	if err = zw.Close(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = os.Chtimes(tmp, fi.ModTime(), fi.ModTime()); err != nil {
		return err
	}
	if err = os.Rename(tmp, path+".gz"); err != nil {
		return err
	}
	return os.Remove(path)
}
