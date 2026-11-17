package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func loopReportToolDef() map[string]interface{} {
	return map[string]interface{}{
		"name":        "loop_report",
		"description": "ループ（Automation）の1回分の結果を記録します。各回の最後に1度呼んでください。Automation が作成したセッションからのみ呼べます。同じ回で複数回呼んだ場合は最後の内容で上書きされます。",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"session_id": sessionIDProperty,
				"metrics": map[string]interface{}{
					"type":                 "object",
					"description":          "指標の key-value（例: {\"pass_rate\": 0.82, \"crashes\": 3}）。値は数値。",
					"additionalProperties": map[string]interface{}{"type": "number"},
				},
				"summary": map[string]interface{}{
					"type":        "string",
					"description": "この回で何をしたか・結果の要約",
				},
				"links": map[string]interface{}{
					"type":        "array",
					"description": "PR・issue・アーティファクトなどのリンク（任意）",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"title": map[string]interface{}{"type": "string"},
							"url":   map[string]interface{}{"type": "string"},
						},
						"required": []string{"title", "url"},
					},
				},
			},
			"required": []string{"metrics", "summary"},
		},
	}
}

func (s *Server) handleLoopReport(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var p struct {
		Metrics map[string]float64 `json:"metrics"`
		Summary string             `json:"summary"`
		Links   []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"links"`
	}
	if err := json.Unmarshal(args, &p); err != nil || strings.TrimSpace(p.Summary) == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "metrics (object of numbers) and summary are required"}
	}
	body, _ := json.Marshal(map[string]interface{}{
		"metrics": p.Metrics, "summary": p.Summary, "links": p.Links,
	})
	resp, err := http.Post(fmt.Sprintf("%s/api/sessions/%s/loop-report", s.apiURL, sessionID), "application/json", bytes.NewReader(body))
	if err != nil {
		return toolResult("loop_report failed: "+err.Error(), true), nil
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return toolResult(fmt.Sprintf("loop_report failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody))), true), nil
	}
	return toolResult("Loop report recorded.", false), nil
}
