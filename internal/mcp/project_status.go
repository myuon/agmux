package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func getProjectStatusToolDef() map[string]interface{} {
	return map[string]interface{}{
		"name":        "get_project_status",
		"description": "自分のセッションが属するプロジェクトの配下の状況を返します。ループ / Automation（状態・トリガー・残タスク数・直近の loop_report・停止理由）、動いているセッション、返事待ちの escalate をまとめて確認できます。コーディネーターが配下を把握するために使います（コーディネーター以外から呼んでも同じプロジェクトの情報が返ります）。",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"session_id": sessionIDProperty,
			},
		},
	}
}

func (s *Server) handleGetProjectStatus(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	resp, err := http.Get(fmt.Sprintf("%s/api/sessions/%s/project-status", s.apiURL, sessionID))
	if err != nil {
		return toolResult("get_project_status failed: "+err.Error(), true), nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return toolResult(fmt.Sprintf("get_project_status failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(body))), true), nil
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return toolResult("get_project_status failed: invalid response", true), nil
	}
	return toolResult(out.Text, false), nil
}
