package logging

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Setup initializes slog with JSON output to both stderr and a log file.
// Returns the log file (caller should defer Close) and the logger.
func Setup() (*os.File, *slog.Logger, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}

	logDir := filepath.Join(home, ".agmux")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, err
	}

	logPath := filepath.Join(logDir, "agmux.log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}

	logger := slog.New(slog.NewJSONHandler(newWriter(os.Stderr, file), &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return file, logger, nil
}

// newWriter returns the destination for daemon logs. When stderr already
// points to the log file (launchd redirects StandardErrorPath to agmux.log),
// writing to both would duplicate every line, so only the file is used.
func newWriter(stderr, file *os.File) io.Writer {
	if sameFile(stderr, file) {
		return file
	}
	return io.MultiWriter(stderr, file)
}

func sameFile(a, b *os.File) bool {
	ai, err := a.Stat()
	if err != nil {
		return false
	}
	bi, err := b.Stat()
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// LogPath returns the default log file path.
func LogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agmux", "agmux.log"), nil
}
