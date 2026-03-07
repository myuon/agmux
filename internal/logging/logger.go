package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// Setup initializes slog with JSON output to both stderr and a log file.
// Additional writers can be provided to tee log output to extra destinations.
// Returns the log file (caller should defer Close) and the logger.
func Setup(extraWriters ...io.Writer) (*os.File, *slog.Logger, error) {
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

	writers := []io.Writer{os.Stderr, file}
	writers = append(writers, extraWriters...)
	writer := io.MultiWriter(writers...)
	logger := slog.New(slog.NewJSONHandler(writer, &slog.HandlerOptions{
		Level: slog.LevelDebug,
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

// ServerLogPath returns the server log file path (~/.agmux/server.log).
func ServerLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".agmux", "server.log"), nil
}

// SetupServerLog opens ~/.agmux/server.log for append and returns the file
// and an io.Writer that tees to both os.Stdout and the file.
// The caller should defer file.Close().
func SetupServerLog() (*os.File, io.Writer, error) {
	logPath, err := ServerLogPath()
	if err != nil {
		return nil, nil, err
	}

	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, err
	}

	writer := io.MultiWriter(os.Stdout, file)
	return file, writer, nil
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
