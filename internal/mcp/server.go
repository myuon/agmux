package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
)

// sessionIDProperty is the shared JSON Schema property for session_id across all tools.
var sessionIDProperty = map[string]interface{}{
	"type":        "string",
	"description": "agmuxのセッションID。",
}

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

// NewServerForHTTP creates an MCP server for use via HTTP transport.
// sessionID and apiURL are provided directly instead of via environment variables.
func NewServerForHTTP(sessionID, apiURL string) *Server {
	return &Server{
		sessionID: sessionID,
		apiURL:    apiURL,
	}
}

// HandleJSONRPC processes a single JSON-RPC request body and returns the response body.
// This is intended for use from an HTTP handler.
func (s *Server) HandleJSONRPC(requestBody []byte) []byte {
	var req jsonRPCRequest
	if err := json.Unmarshal(requestBody, &req); err != nil {
		resp := jsonRPCResponse{
			JSONRPC: "2.0",
			Error:   &jsonRPCError{Code: -32700, Message: "parse error"},
		}
		out, _ := json.Marshal(resp)
		return out
	}

	// Notifications have no id — return empty
	if req.ID == nil {
		return nil
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
	return out
}

// Run starts the MCP server on stdin/stdout (JSON-RPC 2.0).
func (s *Server) Run() error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

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
							"session_id": sessionIDProperty,
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
						"type": "object",
						"properties": map[string]interface{}{
							"session_id": sessionIDProperty,
						},
					},
				},
				map[string]interface{}{
					"name":        "complete_goal",
					"description": "現在のゴールを達成済みとしてポップします。親ゴールがあればそれがアクティブになります。",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"session_id": sessionIDProperty,
						},
					},
				},
				map[string]interface{}{
					"name":        "send_notification",
					"description": "セッション作成者にブラウザ通知を送信します。作業完了の報告やユーザーへの情報共有に使ってください。escalateとは異なり、ユーザーの応答を待たずに即座に返ります。",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"session_id": sessionIDProperty,
							"message": map[string]interface{}{
								"type":        "string",
								"description": "通知メッセージ（例: 「PR #123 を作成しました」「テストが全て通りました」）",
							},
						},
						"required": []string{"message"},
					},
				},
				map[string]interface{}{
					"name":        "escalate",
					"description": "ユーザーへのエスカレーション。判断を仰ぎたいときや確認が必要なときに呼んでください。ブラウザ通知が送られ、ユーザーが応答するまでブロックします。",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"session_id": sessionIDProperty,
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
				map[string]interface{}{
					"name":        "permission_prompt",
					"description": "Handle permission prompts from Claude Code CLI",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"tool_name": map[string]interface{}{
								"type":        "string",
								"description": "The name of the tool requesting permission",
							},
							"input": map[string]interface{}{
								"type":        "object",
								"description": "The input for the tool",
							},
							"tool_use_id": map[string]interface{}{
								"type":        "string",
								"description": "The unique tool use request ID",
							},
						},
						"required": []string{"tool_name", "input"},
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

// resolveSessionID determines the session ID to use for an API call.
// Priority: 1) environment variable AGMUX_SESSION_ID, 2) session_id parameter from tool args.
func (s *Server) resolveSessionID(args json.RawMessage) (string, *jsonRPCError) {
	if s.sessionID != "" {
		return s.sessionID, nil
	}
	// Try to extract session_id from args
	var parsed struct {
		SessionID string `json:"session_id"`
	}
	if len(args) > 0 {
		_ = json.Unmarshal(args, &parsed)
	}
	if parsed.SessionID != "" {
		return parsed.SessionID, nil
	}
	return "", &jsonRPCError{Code: -32602, Message: "session_id is required: set AGMUX_SESSION_ID env var or pass session_id parameter"}
}

func (s *Server) callTool(name string, args json.RawMessage) (interface{}, *jsonRPCError) {
	switch name {
	case "create_goal":
		return s.handleCreateGoal(args)
	case "get_goal":
		return s.handleGetGoal(args)
	case "complete_goal":
		return s.handleCompleteGoal(args)
	case "set_session_context":
		// Backward compatibility
		return s.handleCreateGoal(args)
	case "send_notification":
		return s.handleSendNotification(args)
	case "escalate":
		return s.handleEscalate(args)
	case "permission_prompt":
		return s.handlePermissionPrompt(args)
	default:
		return nil, &jsonRPCError{Code: -32602, Message: "unknown tool: " + name}
	}
}

func (s *Server) handleCreateGoal(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}

	var input struct {
		CurrentTask string `json:"currentTask"`
		Goal        string `json:"goal"`
		Subgoal     bool   `json:"subgoal"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}

	if err := s.apiCreateGoal(sessionID, input.CurrentTask, input.Goal, input.Subgoal); err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}

	msg := "Goal created successfully."
	if input.Subgoal {
		msg = "Subgoal created successfully."
	}
	return toolResult(msg, false), nil
}

func (s *Server) handleGetGoal(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}

	result, err := s.apiGetGoal(sessionID)
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

func (s *Server) handleCompleteGoal(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}

	parentGoal, err := s.apiCompleteGoal(sessionID)
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}

	if parentGoal != "" {
		return toolResult(fmt.Sprintf("Goal completed. Returning to parent goal: %s", parentGoal), false), nil
	}
	return toolResult("Goal completed. No more goals in the stack.", false), nil
}

func (s *Server) handleSendNotification(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}

	var input struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}
	if input.Message == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "message is required"}
	}

	if err := s.apiSendNotification(sessionID, input.Message); err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}

	return toolResult("Notification sent successfully.", false), nil
}

func (s *Server) handleEscalate(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}

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

	response, timedOut, err := s.apiEscalate(sessionID, input.Message, timeoutSeconds)
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}

	if timedOut {
		return toolResult(fmt.Sprintf("[TIMED OUT] %s", response), false), nil
	}
	return toolResult(fmt.Sprintf("User responded: %s", response), false), nil
}


func (s *Server) handlePermissionPrompt(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}

	var input struct {
		ToolName  string          `json:"tool_name"`
		Input     json.RawMessage `json:"input"`
		ToolUseID string          `json:"tool_use_id"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}
	if input.ToolName == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "tool_name is required"}
	}

	response, timedOut, err := s.apiPermissionPrompt(sessionID, input.ToolName, input.Input, input.ToolUseID)
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}

	// For permission_prompt, the response is a JSON string returned as text
	if timedOut || response == "allow" {
		// Auto-allow on timeout
		result := map[string]interface{}{
			"behavior":     "allow",
			"updatedInput": json.RawMessage(input.Input),
		}
		resultJSON, _ := json.Marshal(result)
		return toolResult(string(resultJSON), false), nil
	}

	// User denied
	result := map[string]interface{}{
		"behavior": "deny",
		"message":  "User denied",
	}
	resultJSON, _ := json.Marshal(result)
	return toolResult(string(resultJSON), false), nil
}

func (s *Server) apiPermissionPrompt(sessionID string, toolName string, toolInput json.RawMessage, toolUseID string) (string, bool, error) {
	permissionID := uuid.New().String()
	url := fmt.Sprintf("%s/api/sessions/%s/permission", s.apiURL, sessionID)
	body := fmt.Sprintf(`{"id":%s,"tool_name":%s,"input":%s,"tool_use_id":%s,"timeout_seconds":300}`,
		jsonString(permissionID), jsonString(toolName), string(toolInput), jsonString(toolUseID))

	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Content-Type", "application/json")

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

func (s *Server) apiSendNotification(sessionID string, message string) error {
	url := fmt.Sprintf("%s/api/sessions/%s/notify", s.apiURL, sessionID)
	body := fmt.Sprintf(`{"message":%s}`, jsonString(message))

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

func (s *Server) apiEscalate(sessionID string, message string, timeoutSeconds int) (string, bool, error) {
	escalationID := uuid.New().String()
	url := fmt.Sprintf("%s/api/sessions/%s/escalate", s.apiURL, sessionID)
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

func (s *Server) apiGetGoal(sessionID string) (*goalResponse, error) {
	url := fmt.Sprintf("%s/api/sessions/%s/goals", s.apiURL, sessionID)

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

func (s *Server) apiCreateGoal(sessionID string, currentTask, goal string, subgoal bool) error {
	url := fmt.Sprintf("%s/api/sessions/%s/goals", s.apiURL, sessionID)
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

func (s *Server) apiCompleteGoal(sessionID string) (string, error) {
	url := fmt.Sprintf("%s/api/sessions/%s/goals/complete", s.apiURL, sessionID)

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

func (s *Server) updateContext(sessionID string, currentTask, goal string) error {
	url := fmt.Sprintf("%s/api/sessions/%s/context", s.apiURL, sessionID)
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
