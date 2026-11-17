package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func searchSessionsToolDef() map[string]interface{} {
	return map[string]interface{}{
		"name":        "search_sessions",
		"description": "agmux の全セッション（Claude / Codex / Cursor）の過去の会話を全文検索します。以前に別のセッションで同じ問題をどう解決したか、どんな判断をしたかを思い出したいときに使ってください。空白区切りの語はすべて含むメッセージにマッチします（AND）。結果は新しい順で、ヒット箇所のスニペットとセッション ID を返します。自分自身のセッションは結果から除かれます。",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"session_id": sessionIDProperty,
				"query": map[string]interface{}{
					"type":        "string",
					"description": "検索語（空白区切りで AND 検索）",
				},
				"project": map[string]interface{}{
					"type":        "string",
					"description": "プロジェクトの絶対パスで絞り込む（任意）",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "最大件数（既定 20、最大 100）",
				},
			},
			"required": []string{"query"},
		},
	}
}

type searchResult struct {
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
	ProjectPath string `json:"projectPath"`
	UpdatedAt   string `json:"updatedAt"`
	Role        string `json:"role"`
	Snippet     string `json:"snippet"`
}

func (s *Server) handleSearchSessions(args json.RawMessage) (interface{}, *jsonRPCError) {
	var p struct {
		Query   string `json:"query"`
		Project string `json:"project"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &p); err != nil || strings.TrimSpace(p.Query) == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "query is required"}
	}
	// The caller's own session is excluded; it is optional for search.
	self, _ := s.resolveSessionID(args)

	v := url.Values{}
	v.Set("q", p.Query)
	if p.Project != "" {
		v.Set("project", p.Project)
	}
	if self != "" {
		v.Set("exclude", self)
	}
	if p.Limit > 0 {
		v.Set("limit", strconv.Itoa(p.Limit))
	}
	resp, err := http.Get(fmt.Sprintf("%s/api/search?%s", s.apiURL, v.Encode()))
	if err != nil {
		return toolResult("search failed: "+err.Error(), true), nil
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return toolResult(fmt.Sprintf("search failed (%d): %s", resp.StatusCode, string(body)), true), nil
	}
	var out struct {
		Results  []searchResult `json:"results"`
		Indexing bool           `json:"indexing"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return toolResult("search failed: invalid response", true), nil
	}
	return toolResult(formatSearchResults(p.Query, out.Results, out.Indexing), false), nil
}

func formatSearchResults(query string, results []searchResult, indexing bool) string {
	var b strings.Builder
	if len(results) == 0 {
		fmt.Fprintf(&b, "「%s」に一致するメッセージはありませんでした。", query)
	} else {
		fmt.Fprintf(&b, "「%s」の検索結果 %d 件（新しい順）:\n", query, len(results))
		for _, r := range results {
			date := r.UpdatedAt
			if len(date) >= 10 {
				date = date[:10]
			}
			fmt.Fprintf(&b, "\n- [%s] %s (session_id: %s, %s, %s)\n  %s\n", r.Role, r.SessionName, r.SessionID, r.ProjectPath, date, r.Snippet)
		}
		b.WriteString("\nセッションの詳細は `agmux session history <session_id>` で読めます。")
	}
	if indexing {
		b.WriteString("\n（注: 索引を作成中のため、結果が不完全な可能性があります）")
	}
	return b.String()
}
