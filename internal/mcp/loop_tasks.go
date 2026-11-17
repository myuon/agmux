package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// loopToolDefs returns the loop_task_* tool definitions. They are gated by
// the "loops" experimental feature (see experimentalTools).
func loopToolDefs() []interface{} {
	return []interface{}{
		map[string]interface{}{
			"name":        "loop_task_add",
			"description": "このループ（Automation）の残タスクリストにタスクを追加します。作業中に見つけた、今回の範囲外のやるべき作業を足すときに使ってください。Automation から起動されたセッションでのみ使えます。",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"session_id": sessionIDProperty,
					"title":      map[string]interface{}{"type": "string", "description": "タスクの題名"},
					"body":       map[string]interface{}{"type": "string", "description": "タスクの詳細（任意）"},
				},
				"required": []string{"title"},
			},
		},
		map[string]interface{}{
			"name":        "loop_task_next",
			"description": "このループの残タスクリストから、pending の最古の1件を取り出して in_progress にして返します（この Run に紐づきます）。残タスクが無ければ空であることを返します。取り出したタスクは作業後に必ず loop_task_done で完了/失敗を報告してください。",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"session_id": sessionIDProperty,
				},
			},
		},
		map[string]interface{}{
			"name":        "loop_task_done",
			"description": "loop_task_next で取り出したタスクを完了(done)または失敗(failed)にして、結果を記録します。",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"session_id": sessionIDProperty,
					"task_id":    map[string]interface{}{"type": "integer", "description": "loop_task_next が返したタスク ID"},
					"status":     map[string]interface{}{"type": "string", "enum": []string{"done", "failed"}, "description": "done: 完了 / failed: 失敗"},
					"result":     map[string]interface{}{"type": "string", "description": "結果の要約（何をしたか、失敗した理由など）"},
				},
				"required": []string{"task_id", "status"},
			},
		},
	}
}

type loopTask struct {
	ID     int64  `json:"id"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Status string `json:"status"`
}

func (s *Server) loopAPI(sessionID, action string, payload interface{}) ([]byte, string) {
	b, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/api/sessions/%s/loop-tasks/%s", s.apiURL, sessionID, action)
	resp, err := http.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, "request failed: " + err.Error()
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		var e struct {
			Error string `json:"error"`
		}
		msg := string(body)
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			msg = e.Error
		}
		return nil, msg
	}
	return body, ""
}

func (s *Server) handleLoopTaskAdd(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var in struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.Title == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "title is required"}
	}
	body, errMsg := s.loopAPI(sessionID, "add", map[string]string{"title": in.Title, "body": in.Body})
	if errMsg != "" {
		return toolResult("loop_task_add failed: "+errMsg, true), nil
	}
	var t loopTask
	_ = json.Unmarshal(body, &t)
	return toolResult(fmt.Sprintf("残タスクに追加しました (task_id: %d): %s", t.ID, t.Title), false), nil
}

func (s *Server) handleLoopTaskNext(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	body, errMsg := s.loopAPI(sessionID, "next", map[string]string{})
	if errMsg != "" {
		return toolResult("loop_task_next failed: "+errMsg, true), nil
	}
	var out struct {
		Task *loopTask `json:"task"`
	}
	_ = json.Unmarshal(body, &out)
	if out.Task == nil {
		return toolResult("残タスクはありません。", false), nil
	}
	text := fmt.Sprintf("task_id: %d\ntitle: %s", out.Task.ID, out.Task.Title)
	if out.Task.Body != "" {
		text += "\nbody:\n" + out.Task.Body
	}
	text += "\n\n作業が終わったら loop_task_done(task_id, status, result) で報告してください。"
	return toolResult(text, false), nil
}

func (s *Server) handleLoopTaskDone(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var in struct {
		TaskID int64  `json:"task_id"`
		Status string `json:"status"`
		Result string `json:"result"`
	}
	if err := json.Unmarshal(args, &in); err != nil || in.TaskID == 0 {
		return nil, &jsonRPCError{Code: -32602, Message: "task_id is required"}
	}
	_, errMsg := s.loopAPI(sessionID, "done", map[string]interface{}{"taskId": in.TaskID, "status": in.Status, "result": in.Result})
	if errMsg != "" {
		return toolResult("loop_task_done failed: "+errMsg, true), nil
	}
	return toolResult(fmt.Sprintf("タスク %d を %s にしました。", in.TaskID, in.Status), false), nil
}
