package session

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/myuon/agmux/internal/db"
)

// RestartRequester holds the session ID that requested a server restart.
type RestartRequester struct {
	SessionID string `json:"sessionId"`
}

func restartRequesterPath() (string, error) {
	dir, err := db.AgmuxDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "restart_requester.json"), nil
}

// SaveRestartRequester persists the requester session ID so the server
// can notify it after restart.
func SaveRestartRequester(sessionID string) error {
	path, err := restartRequesterPath()
	if err != nil {
		return err
	}
	data, err := json.Marshal(RestartRequester{SessionID: sessionID})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// LoadRestartRequester reads and removes the restart requester file.
// Returns empty string if the file does not exist.
func LoadRestartRequester() (string, error) {
	path, err := restartRequesterPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	// Remove immediately so it's only consumed once
	os.Remove(path)

	var req RestartRequester
	if err := json.Unmarshal(data, &req); err != nil {
		return "", err
	}
	return req.SessionID, nil
}
