package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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
					"name":        "complete_goal",
					"description": "現在のゴールを達成済みとしてポップします。親ゴールがあればそれがアクティブになります。",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{},
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
	case "complete_goal":
		return s.handleCompleteGoal()
	case "set_session_context":
		// Backward compatibility
		return s.handleCreateGoal(args)
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

func (s *Server) handleCompleteGoal() (interface{}, *jsonRPCError) {
	parent, err := s.apiCompleteGoal()
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}

	if parent != "" {
		return toolResult(fmt.Sprintf("Goal completed. Returning to parent goal: %s", parent), false), nil
	}
	return toolResult("Goal completed. No more goals in the stack.", false), nil
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
