package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/myuon/agmux/internal/artifact"
)

// handlePublishArtifact copies an HTML file into the artifacts dir (so later
// edits to the original don't change what the user sees) and registers it.
func (s *Server) handlePublishArtifact(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var input struct {
		Path  string `json:"path"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "title is required"}
	}
	if input.Path == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "path is required"}
	}

	dir, err := artifact.DefaultDir()
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}
	id, err := artifact.Publish(dir, input.Path)
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}
	url, err := s.apiRegisterArtifact(sessionID, id, input.Title)
	if err != nil {
		_ = artifact.RemoveFiles(dir, id)
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}
	return toolResult(fmt.Sprintf("HTML を公開しました。agmux 上の表示パス: %s\n公開時点の内容がコピーされています（元ファイルを後で変更しても反映されません）。相対パスのアセット（画像・CSS・JS ファイル）は同梱されないため、HTML は自己完結（インライン、または https の CDN）にしてください。ユーザーにはブラウザ通知が送られました。", url), false), nil
}

func (s *Server) apiRegisterArtifact(sessionID, id, title string) (string, error) {
	body, _ := json.Marshal(map[string]string{"id": id, "title": title})
	url := fmt.Sprintf("%s/api/sessions/%s/artifacts", s.apiURL, sessionID)
	req, err := http.NewRequest("POST", url, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}
	var out struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil || out.URL == "" {
		return "", fmt.Errorf("unexpected API response")
	}
	return out.URL, nil
}
