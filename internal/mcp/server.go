package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"github.com/google/uuid"
)

type Server struct {
	sessionID string
	apiURL    string
}

func NewServer() *Server {
	return &Server{
		sessionID: os.Getenv("AGMUX_SESSION_ID"),
		apiURL:    os.Getenv("AGMUX_API_URL"),
	}
}

// Run starts the MCP server on stdin/stdout (JSON-RPC 2.0).
func (s *Server) Run() error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}

		// Notifications have no id — no response needed
		if req.ID == nil {
			continue
		}

		result, rpcErr := s.handleMethod(req.Method, req.Params)
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
		}
		if rpcErr != nil {
			resp.Error = rpcErr
		} else {
			raw, _ := json.Marshal(result)
			rawMsg := json.RawMessage(raw)
			resp.Result = &rawMsg
		}

		out, _ := json.Marshal(resp)
		fmt.Fprintf(os.Stdout, "%s\n", out)
	}

	return scanner.Err()
}

func (s *Server) handleMethod(method string, params json.RawMessage) (interface{}, *jsonRPCError) {
	switch method {
	case "initialize":
		return map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "agmux",
				"version": "1.0.0",
			},
		}, nil

	case "tools/list":
		return map[string]interface{}{
			"tools": []interface{}{
				map[string]interface{}{
					"name":        "create_goal",
					"description": "agmuxの管理画面に表示されるゴールを設定します。新しいタスクに着手するときに呼んでください。subgoal=trueにすると現在のゴールを保持したままサブゴールを積めます。",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"currentTask": map[string]interface{}{
								"type":        "string",
								"description": "今取り組んでいる作業の概要（例: 「ログイン画面のバリデーション実装」）",
							},
							"goal": map[string]interface{}{
								"type":        "string",
								"description": "この作業の完了条件（例: 「メールとパスワードのバリデーションが動作しテストが通る」）",
							},
							"subgoal": map[string]interface{}{
								"type":        "boolean",
								"description": "trueにすると現在のゴールを保持しサブゴールとしてスタックに積む。falseまたは省略で現在のゴールを上書き。",
								"default":     false,
							},
						},
						"required": []string{"currentTask", "goal"},
					},
				},
				map[string]interface{}{
					"name":        "get_goal",
					"description": "現在のゴール情報を取得します。currentTask、goal、goalsスタックを返します。コンテキストが圧縮された後などに自分が何をやっていたか確認するのに使えます。",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				map[string]interface{}{
					"name":        "complete_goal",
					"description": "現在のゴールを達成済みとしてポップします。親ゴールがあればそれがアクティブになります。",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				map[string]interface{}{
					"name":        "restart_server",
					"description": "agmuxサーバーを再ビルド・再起動します。再起動完了後、このセッションに自動通知されます。",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
					},
				},
				map[string]interface{}{
					"name":        "escalate",
					"description": "ユーザーへのエスカレーション。判断を仰ぎたいときや確認が必要なときに呼んでください。ブラウザ通知が送られ、ユーザーが応答するまでブロックします。",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"message": map[string]interface{}{
								"type":        "string",
								"description": "ユーザーに伝えたいメッセージ（例: 「テストが3件失敗しています。修正方針を教えてください」）",
							},
							"timeout_seconds": map[string]interface{}{
								"type":        "integer",
								"description": "タイムアウト秒数。指定時間内にユーザーが応答しない場合、自動的にあなたの判断で進めるよう指示されます。デフォルト: 300秒（5分）",
								"default":     300,
							},
						},
						"required": []string{"message"},
					},
				},
			},
		}, nil

	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &jsonRPCError{Code: -32602, Message: "invalid params"}
		}
		return s.callTool(p.Name, p.Arguments)

	default:
		return nil, &jsonRPCError{Code: -32601, Message: "method not found"}
	}
}

func (s *Server) callTool(name string, args json.RawMessage) (interface{}, *jsonRPCError) {
	switch name {
	case "create_goal":
		return s.handleCreateGoal(args)
	case "get_goal":
		return s.handleGetGoal()
	case "complete_goal":
		return s.handleCompleteGoal()
	case "set_session_context":
		// Backward compatibility
		return s.handleCreateGoal(args)
	case "escalate":
		return s.handleEscalate(args)
	case "restart_server":
		return s.handleRestartServer()
	default:
		return nil, &jsonRPCError{Code: -32602, Message: "unknown tool: " + name}
	}
}

func (s *Server) handleCreateGoal(args json.RawMessage) (interface{}, *jsonRPCError) {
	var input struct {
		CurrentTask string `json:"currentTask"`
		Goal        string `json:"goal"`
		Subgoal     bool   `json:"subgoal"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}

	if err := s.apiCreateGoal(input.CurrentTask, input.Goal, input.Subgoal); err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}

	msg := "Goal created successfully."
	if input.Subgoal {
		msg = "Subgoal created successfully."
	}
	return toolResult(msg, false), nil
}

func (s *Server) handleGetGoal() (interface{}, *jsonRPCError) {
	result, err := s.apiGetGoal()
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}

	if result.CurrentTask == "" && result.Goal == "" && len(result.Goals) == 0 {
		return toolResult("No goal is currently set.", false), nil
	}

	lines := []string{}
	lines = append(lines, fmt.Sprintf("currentTask: %s", result.CurrentTask))
	lines = append(lines, fmt.Sprintf("goal: %s", result.Goal))
	if len(result.Goals) > 0 {
		lines = append(lines, fmt.Sprintf("goal stack (%d entries):", len(result.Goals)))
		for i, g := range result.Goals {
			lines = append(lines, fmt.Sprintf("  [%d] %s — %s", i, g.CurrentTask, g.Goal))
		}
	}
	return toolResult(strings.Join(lines, "\n"), false), nil
}

func (s *Server) handleCompleteGoal() (interface{}, *jsonRPCError) {
	parentGoal, err := s.apiCompleteGoal()
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}

	if parentGoal != "" {
		return toolResult(fmt.Sprintf("Goal completed. Returning to parent goal: %s", parentGoal), false), nil
	}
	return toolResult("Goal completed. No more goals in the stack.", false), nil
}

func (s *Server) handleEscalate(args json.RawMessage) (interface{}, *jsonRPCError) {
	var input struct {
		Message        string `json:"message"`
		TimeoutSeconds *int   `json:"timeout_seconds,omitempty"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}
	if input.Message == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "message is required"}
	}

	timeoutSeconds := 300 // default 5 minutes
	if input.TimeoutSeconds != nil {
		timeoutSeconds = *input.TimeoutSeconds
	}

	response, timedOut, err := s.apiEscalate(input.Message, timeoutSeconds)
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}

	if timedOut {
		return toolResult(fmt.Sprintf("[TIMED OUT] %s", response), false), nil
	}
	return toolResult(fmt.Sprintf("User responded: %s", response), false), nil
}

func (s *Server) handleRestartServer() (interface{}, *jsonRPCError) {
	if err := s.apiRestartServer(); err != nil {
		// API call failed (server may be down); fall back to launchctl kickstart
		if kickErr := s.launchctlKickstart(); kickErr != nil {
			return toolResult(fmt.Sprintf("Error: API=%v, launchctl=%v", err, kickErr), true), nil
		}
		return toolResult("APIへの接続に失敗しましたが、launchctlで再起動をキックしました。しばらくお待ちください。", false), nil
	}

	// Also run launchctl kickstart to ensure the service is restarted
	_ = s.launchctlKickstart()

	return toolResult("サーバーの再起動を開始しました。再ビルド・再起動が完了すると、このセッションに自動通知されます。しばらくお待ちください。", false), nil
}

func (s *Server) launchctlKickstart() error {
	uid, err := exec.Command("id", "-u").Output()
	if err != nil {
		return fmt.Errorf("failed to get uid: %w", err)
	}
	uidStr := strings.TrimSpace(string(uid))
	serviceTarget := fmt.Sprintf("gui/%s/com.myuon.agmux", uidStr)
	return exec.Command("launchctl", "kickstart", "-k", serviceTarget).Run()
}

func (s *Server) apiRestartServer() error {
	url := fmt.Sprintf("%s/api/restart", s.apiURL)
	body := fmt.Sprintf(`{"sessionId":%s}`, jsonString(s.sessionID))

	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (s *Server) apiEscalate(message string, timeoutSeconds int) (string, bool, error) {
	escalationID := uuid.New().String()
	url := fmt.Sprintf("%s/api/sessions/%s/escalate", s.apiURL, s.sessionID)
	body := fmt.Sprintf(`{"id":%s,"message":%s,"timeout_seconds":%d}`,
		jsonString(escalationID), jsonString(message), timeoutSeconds)

	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")

	// Use a client without timeout since this call blocks until user responds
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", false, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Response string `json:"response"`
		TimedOut bool   `json:"timed_out"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", false, fmt.Errorf("decode response: %w", err)
	}
	return result.Response, result.TimedOut, nil
}

type goalResponse struct {
	CurrentTask string `json:"currentTask"`
	Goal        string `json:"goal"`
	Goals       []struct {
		CurrentTask string `json:"currentTask"`
		Goal        string `json:"goal"`
	} `json:"goals"`
}

func (s *Server) apiGetGoal() (*goalResponse, error) {
	url := fmt.Sprintf("%s/api/sessions/%s/goals", s.apiURL, s.sessionID)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result goalResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

func (s *Server) apiCreateGoal(currentTask, goal string, subgoal bool) error {
	url := fmt.Sprintf("%s/api/sessions/%s/goals", s.apiURL, s.sessionID)
	body := fmt.Sprintf(`{"currentTask":%s,"goal":%s,"subgoal":%t}`,
		jsonString(currentTask), jsonString(goal), subgoal)

	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func (s *Server) apiCompleteGoal() (string, error) {
	url := fmt.Sprintf("%s/api/sessions/%s/goals/complete", s.apiURL, s.sessionID)

	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ParentGoal *struct {
			Goal string `json:"goal"`
		} `json:"parentGoal"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", nil
	}
	if result.ParentGoal != nil {
		return result.ParentGoal.Goal, nil
	}
	return "", nil
}

func (s *Server) updateContext(currentTask, goal string) error {
	url := fmt.Sprintf("%s/api/sessions/%s/context", s.apiURL, s.sessionID)
	body := fmt.Sprintf(`{"currentTask":%s,"goal":%s}`,
		jsonString(currentTask), jsonString(goal))

	req, err := http.NewRequest("PUT", url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func toolResult(text string, isError bool) interface{} {
	return map[string]interface{}{
		"content": []interface{}{
			map[string]interface{}{
				"type": "text",
				"text": text,
			},
		},
		"isError": isError,
	}
}

// JSON-RPC types

type jsonRPCRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  *json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError    `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
