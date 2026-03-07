package session

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/myuon/agmux/internal/db"
)

type mcpConfig struct {
	McpServers map[string]mcpServerEntry `json:"mcpServers"`
}

type mcpServerEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env,omitempty"`
}

const agmuxSystemPrompt = `あなたはagmuxで管理されているセッションです。以下のルールを守ってください:
- 新しいタスクに着手するとき、create_goal ツールで currentTask と goal を設定してください
- タスクの内容や目標が変わったら、その都度 create_goal を呼び出して更新してください
- タスクが完了したら、complete_goal で完了状態を反映してください
- サブタスクが発生した場合は create_goal の subgoal=true で親ゴールを保持したままサブゴールを設定してください
- サブタスクが完了したら complete_goal でポップし、親ゴールに戻ってください
- ユーザーに判断を仰ぎたいときや確認が必要なときは escalate ツールを使ってください。ブラウザ通知が送られ、ユーザーが応答するまで待機します`

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// writeMCPConfig generates a temporary MCP config JSON file for a session.
// Returns the path to the config file.
func writeMCPConfig(sessionID string, apiPort int) (string, error) {
	dir, err := db.AgmuxDir()
	if err != nil {
		return "", err
	}
	mcpDir := filepath.Join(dir, "mcp-configs")
	if err := os.MkdirAll(mcpDir, 0o755); err != nil {
		return "", fmt.Errorf("create mcp-configs dir: %w", err)
	}

	agmuxPath, err := exec.LookPath("agmux")
	if err != nil {
		// Fallback to "agmux" if not found in PATH
		agmuxPath = "agmux"
	}

	cfg := mcpConfig{
		McpServers: map[string]mcpServerEntry{
			"agmux": {
				Command: agmuxPath,
				Args:    []string{"mcp"},
				Env: map[string]string{
					"AGMUX_SESSION_ID": sessionID,
					"AGMUX_API_URL":    fmt.Sprintf("http://localhost:%d", apiPort),
				},
			},
		},
	}

	path := filepath.Join(mcpDir, sessionID+".json")
	f, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create mcp config file: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cfg); err != nil {
		return "", fmt.Errorf("encode mcp config: %w", err)
	}

	return path, nil
}
