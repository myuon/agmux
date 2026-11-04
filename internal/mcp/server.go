package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/myuon/agmux/internal/linereader"
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
	reader := linereader.New(os.Stdin, linereader.Options{})

	for {
		l, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if l.Oversized {
			// Cannot be answered (no id available); skip it and keep serving.
			slog.Warn("mcp: oversized request line skipped", "bytes", l.Size)
			continue
		}
		if len(l.Data) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(l.Data, &req); err != nil {
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
					"description": "現在のゴールを達成済みとしてポップします。親ゴールがあればそれがアクティブになります。reportを指定すると完了レポートとして保存されます。",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"session_id": sessionIDProperty,
							"report": map[string]interface{}{
								"type":        "string",
								"description": "完了時のレポート文字列（任意）",
							},
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
					"name":        "get_logged_in_browser",
					"description": "ユーザーのログイン状態を共有した Chrome（agmux 管理のリモートブラウザ）に、このセッション専用のタブを割り当て、agent-browser で操作するための接続情報とコマンド形式を返します。ユーザーがログイン済みのサイトを閲覧・操作する必要があるとき（ログインが必要なページなど）にのみ使ってください。一般的な Web 閲覧・調査・テストには使わず、通常どおり agent-browser を単独で使ってください（このブラウザのプロファイルにはユーザーのログイン情報が入っています）。同じセッションで再度呼ぶと同じタブが返ります。ログインページにリダイレクトされた場合は request_browser_login を呼んでください。",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"session_id": sessionIDProperty,
						},
					},
				},
				map[string]interface{}{
					"name":        "request_browser_login",
					"description": "ログイン切れを検知したとき（get_logged_in_browser のタブでログインページにリダイレクトされた等）に、ユーザーへ再ログインを依頼します。ユーザーにブラウザ通知が送られ、タップするとリモートブラウザ画面で指定 URL が開きます。ユーザーが「ログイン完了」または「キャンセル」を押すかタイムアウトするまでブロックします。このツールはログインの成否を検証しません。「完了」が返ったら、自分のタブで対象ページを開き直してログインできたかを必ず確認してください。ユーザーのパスワード等を自分で入力・取得しようとしないでください。",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"session_id": sessionIDProperty,
							"url": map[string]interface{}{
								"type":        "string",
								"description": "ユーザーにログインしてもらうページの URL（http/https のみ）",
							},
							"reason": map[string]interface{}{
								"type":        "string",
								"description": "ユーザーに表示する短い理由（例: 「example.com のログインが切れました」）",
							},
							"timeout_seconds": map[string]interface{}{
								"type":        "integer",
								"description": "タイムアウト秒数。デフォルト: 600秒（10分）",
								"default":     600,
							},
						},
						"required": []string{"url", "reason"},
					},
				},
				map[string]interface{}{
					"name":        "publish_artifact",
					"description": "作成した HTML ファイル（1ファイル）を agmux に登録し、ユーザーがスマホ等から全画面で閲覧できるようにします。呼び出し時点の内容がコピーされ、ユーザーにブラウザ通知が送られます。公開済みの HTML を変更するときは、元の HTML ファイルを編集してから artifact_id を指定して再度呼ぶと同じ URL のまま内容が差し替わります（~/.agmux/artifacts 配下は直接編集しないこと）。表示は sandbox + CSP で隔離され、agmux の API や外部への通信（fetch/WebSocket/フォーム送信）はできません。相対パスのアセット（画像・CSS・JS ファイル）は同梱されないため、HTML は自己完結（インライン、または https の CDN からの読み込み）にしてください。.html / .htm のみ、20 MiB まで。",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"session_id": sessionIDProperty,
							"path": map[string]interface{}{
								"type":        "string",
								"description": "公開する HTML ファイルの絶対パス（.html / .htm）",
							},
							"title": map[string]interface{}{
								"type":        "string",
								"description": "一覧と閲覧画面に表示するタイトル。新規公開では必須。artifact_id 指定時は省略すると既存のタイトルのまま",
							},
							"artifact_id": map[string]interface{}{
								"type":        "string",
								"description": "更新する公開済みアーティファクトの id（公開時の結果に表示される）。指定すると新規公開せず、その id の内容を path のファイルで差し替える",
							},
						},
						"required": []string{"path"},
					},
				},
				map[string]interface{}{
					"name":        "create_automation",
					"description": "定期実行のAutomation（スケジュールに従って新しいセッションを起動しプロンプトを実行する自動化）を作成します。ユーザーから定期実行タスクの設定を頼まれたとき、または会話の流れでAutomation化に合意できたときに呼んでください。作成前に名前・トリガー・プロンプト内容をユーザーと確認し、判断に迷う場合は escalate ツールで確認してから呼ぶこと。作成されたAutomationは設定画面（Settings > Automations）に表示されます。",
					"inputSchema": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"session_id": sessionIDProperty,
							"name": map[string]interface{}{
								"type":        "string",
								"description": "Automationの名前（例: 「毎朝のイシュー整理」）",
							},
							"prompt": map[string]interface{}{
								"type":        "string",
								"description": "発動時にエージェントへ渡す指示文（例: 「openなissueを確認し、優先度順に並べてレポートしてください」）",
							},
							"triggerType": map[string]interface{}{
								"type":        "string",
								"enum":        []string{"interval", "cron"},
								"description": "トリガー種別。interval: 固定間隔、cron: cron式スケジュール。",
							},
							"triggerValue": map[string]interface{}{
								"type":        "string",
								"description": "トリガーの値。interval の場合はGoのduration文字列（例: \"30m\", \"1h\"）、cron の場合は5フィールドのcron式（例: \"0 9 * * 1-5\"）。",
							},
							"projectPath": map[string]interface{}{
								"type":        "string",
								"description": "セッションを起動するプロジェクトの作業ディレクトリ（絶対パス）。省略時はcontroller領域で実行されます。",
							},
							"model": map[string]interface{}{
								"type":        "string",
								"description": "セッションで使うモデル名（任意）。省略時はプロバイダの既定モデル。単純な機械的作業（コマンド実行のみ等）には軽量モデル（例: claude-haiku-5-5）を指定するとよい。",
							},
							"enabled": map[string]interface{}{
								"type":        "boolean",
								"description": "作成時に有効化するかどうか。省略時は true。",
								"default":     true,
							},
						},
						"required": []string{"name", "prompt", "triggerType", "triggerValue"},
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
	case "create_automation":
		return s.handleCreateAutomation(args)
	case "get_logged_in_browser":
		return s.handleGetLoggedInBrowser(args)
	case "request_browser_login":
		return s.handleRequestBrowserLogin(args)
	case "publish_artifact":
		return s.handlePublishArtifact(args)
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

	var input struct {
		Report string `json:"report"`
	}
	_ = json.Unmarshal(args, &input) // report is optional; ignore error (defaults to empty string)

	parentGoal, err := s.apiCompleteGoal(sessionID, input.Report)
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

	response, timedOut, err := s.apiEscalate(sessionID, input.Message, timeoutSeconds, "")
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}

	if timedOut {
		return toolResult(fmt.Sprintf("[TIMED OUT] %s", response), false), nil
	}
	return toolResult(fmt.Sprintf("User responded: %s", response), false), nil
}

func (s *Server) handleCreateAutomation(args json.RawMessage) (interface{}, *jsonRPCError) {
	var input struct {
		Name         string `json:"name"`
		Prompt       string `json:"prompt"`
		TriggerType  string `json:"triggerType"`
		TriggerValue string `json:"triggerValue"`
		ProjectPath  string `json:"projectPath"`
		Model        string `json:"model"`
		Enabled      *bool  `json:"enabled"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}
	if input.Name == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "name is required"}
	}
	if input.Prompt == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "prompt is required"}
	}
	if input.TriggerType != "interval" && input.TriggerType != "cron" {
		return nil, &jsonRPCError{Code: -32602, Message: `triggerType must be "interval" or "cron"`}
	}
	if input.TriggerValue == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "triggerValue is required"}
	}

	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	created, err := s.apiCreateAutomation(input.Name, input.Prompt, input.TriggerType, input.TriggerValue, input.ProjectPath, input.Model, enabled)
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}

	target := created.ProjectPath
	if target == "" {
		target = "controller"
	}
	msg := fmt.Sprintf("Automation created successfully: %s (id: %s, trigger: %s %s, project: %s, enabled: %t)",
		created.Name, created.ID, created.TriggerType, created.TriggerValue, target, created.Enabled)
	return toolResult(msg, false), nil
}

type automationResponse struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	TriggerType  string `json:"triggerType"`
	TriggerValue string `json:"triggerValue"`
	ProjectPath  string `json:"projectPath"`
	Enabled      bool   `json:"enabled"`
}

func (s *Server) apiCreateAutomation(name, prompt, triggerType, triggerValue, projectPath, model string, enabled bool) (*automationResponse, error) {
	url := fmt.Sprintf("%s/api/automations", s.apiURL)
	body := fmt.Sprintf(`{"name":%s,"prompt":%s,"triggerType":%s,"triggerValue":%s,"projectPath":%s,"model":%s,"enabled":%t}`,
		jsonString(name), jsonString(prompt), jsonString(triggerType), jsonString(triggerValue), jsonString(projectPath), jsonString(model), enabled)

	req, err := http.NewRequest("POST", url, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	var result automationResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
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

func (s *Server) apiEscalate(sessionID string, message string, timeoutSeconds int, loginURL string) (string, bool, error) {
	escalationID := uuid.New().String()
	url := fmt.Sprintf("%s/api/sessions/%s/escalate", s.apiURL, sessionID)
	body := fmt.Sprintf(`{"id":%s,"message":%s,"timeout_seconds":%d`,
		jsonString(escalationID), jsonString(message), timeoutSeconds)
	if loginURL != "" {
		body += fmt.Sprintf(`,"login_url":%s`, jsonString(loginURL))
	}
	body += "}"

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

func (s *Server) apiCompleteGoal(sessionID string, report string) (string, error) {
	url := fmt.Sprintf("%s/api/sessions/%s/goals/complete", s.apiURL, sessionID)

	var bodyReader io.Reader
	if report != "" {
		body := fmt.Sprintf(`{"report":%s}`, jsonString(report))
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequest("POST", url, bodyReader)
	if err != nil {
		return "", err
	}
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
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
