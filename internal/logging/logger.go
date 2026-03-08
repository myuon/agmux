package logging

import (
	"fmt"
	"io"
	stdlog "log"
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

// SetupServerLog creates a *log.Logger that writes to both os.Stdout and ~/.agmux/server.log.
// Returns the log file (caller should defer Close) and the logger.
func SetupServerLog() (*os.File, *stdlog.Logger, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, nil, err
	}

	logDir := filepath.Join(home, ".agmux")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, err
	}

	logPath := filepath.Join(logDir, "server.log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}

	writer := io.MultiWriter(os.Stdout, file)
	logger := stdlog.New(writer, "", stdlog.LstdFlags)
	return file, logger, nil
}

// LogAction logs an action entry with category:"action" to distinguish from general logs.
func LogAction(logger *slog.Logger, sessionID, actionType, detail, source string, extra ...slog.Attr) {
	msg := fmt.Sprintf("[action] %s", actionType)
	if detail != "" {
		msg += ": " + detail
	}

	attrs := []slog.Attr{
		slog.String("category", "action"),
		slog.String("sessionId", sessionID),
	}
	attrs = append(attrs, extra...)

	args := make([]any, len(attrs))
	for i, a := range attrs {
		args[i] = a
	}
	logger.Info(msg, args...)
}
