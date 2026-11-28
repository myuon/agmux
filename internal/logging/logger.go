package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Setup initializes slog with JSON output to both stderr and a rotating log
// file (~/.agmux/agmux.log). logDays is the retention for rotated logs
// (<= 0 keeps them forever). The returned writer must be closed by the caller.
func Setup(logDays int) (io.Closer, *slog.Logger, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}

	logDir := filepath.Join(home, ".agmux")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, err
	}

	// launchd points stderr (and stdout) at agmux.log. Detect that before the
	// file is rotated so that fd 1/2 can be re-pointed after each rotation.
	same := stderrIsFile(os.Stderr, filepath.Join(logDir, "agmux.log"))
	rf, err := openRotating(logDir, rotateOptions{days: logDays, redirect: same})
	if err != nil {
		return nil, nil, err
	}

	logger := slog.New(slog.NewJSONHandler(newWriter(os.Stderr, rf, same), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return rf, logger, nil
}

func stderrIsFile(stderr *os.File, path string) bool {
	pi, err := os.Stat(path)
	if err != nil {
		return false
	}
	si, err := stderr.Stat()
	if err != nil {
		return false
	}
	return os.SameFile(pi, si)
}

// newWriter returns the destination for daemon logs. When stderr already
// points to the log file (launchd redirects StandardErrorPath to agmux.log),
// writing to both would duplicate every line, so only the file is used.
func newWriter(stderr *os.File, file io.Writer, same bool) io.Writer {
	if same {
		return file
	}
	return io.MultiWriter(stderr, file)
}

// LogPath returns the default log file path.
func LogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agmux", "agmux.log"), nil
}
