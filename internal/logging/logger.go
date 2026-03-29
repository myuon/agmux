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

	writer := io.MultiWriter(os.Stderr, file)
	logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	return file, logger, nil
}

// LogPath returns the default log file path.
func LogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agmux", "agmux.log"), nil
}

