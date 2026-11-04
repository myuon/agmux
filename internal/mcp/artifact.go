package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/myuon/agmux/internal/artifact"
)

// handlePublishArtifact copies an HTML file into the artifacts dir (so later
// edits to the original don't change what the user sees) and registers it.
// With artifact_id it instead replaces the content of that existing artifact.
func (s *Server) handlePublishArtifact(args json.RawMessage) (interface{}, *jsonRPCError) {
	sessionID, rpcErr := s.resolveSessionID(args)
	if rpcErr != nil {
		return nil, rpcErr
	}
	var input struct {
		Path       string `json:"path"`
		Title      string `json:"title"`
		ArtifactID string `json:"artifact_id"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "invalid arguments"}
	}
	input.Title = strings.TrimSpace(input.Title)
	input.ArtifactID = strings.TrimSpace(input.ArtifactID)
	if input.Path == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "path is required"}
	}
	if input.ArtifactID != "" {
		return s.updateArtifact(sessionID, input.ArtifactID, input.Path, input.Title), nil
	}
	if input.Title == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "title is required"}
	}

	dir, err := artifact.DefaultDir()
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}
	id, err := artifact.Publish(dir, input.Path)
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}
	viewPath, err := s.apiRegisterArtifact(sessionID, id, input.Title)
	if err != nil {
		_ = artifact.RemoveFiles(dir, id)
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}
	return toolResult(fmt.Sprintf("HTML を公開しました。\nartifact_id: %s\nagmux 上の表示パス: %s\n公開時点の内容がコピーされています（元ファイルを後で変更しても反映されません）。内容を変更したいときは、元の HTML を編集してから artifact_id を指定して publish_artifact を再度呼んでください（同じ URL のまま差し替わります。~/.agmux/artifacts 配下は直接編集しないこと）。相対パスのアセット（画像・CSS・JS ファイル）は同梱されないため、HTML は自己完結（インライン、または https の CDN）にしてください。ユーザーにはブラウザ通知が送られました。", id, viewPath), false), nil
}

// updateArtifact replaces an existing artifact's content under the same id.
// Order: check existence via the API (nothing written for unknown ids), swap
// the file atomically (keeping a backup), tell the server, and roll the file
// back if the server refuses so file and metadata stay consistent.
func (s *Server) updateArtifact(sessionID, id, path, title string) interface{} {
	if !artifact.ValidID(id) {
		return toolResult("Error: artifact_id is invalid (expected the 32-character id returned when it was published)", true)
	}
	dir, err := artifact.DefaultDir()
	if err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true)
	}
	unlock := artifact.LockUpdate(id)
	defer unlock()

	if err := s.apiCheckArtifact(id); err != nil {
		return toolResult(fmt.Sprintf("Error: %v", err), true)
	}
	rep, err := artifact.Replace(dir, id, path)
	if err != nil {
		if errors.Is(err, artifact.ErrNotFound) {
			err = fmt.Errorf("artifact %s was not found", id)
		}
		return toolResult(fmt.Sprintf("Error: %v", err), true)
	}
	viewPath, err := s.apiUpdateArtifact(sessionID, id, title)
	if err != nil {
		if rbErr := rep.Rollback(); rbErr != nil {
			return toolResult(fmt.Sprintf("Error: %v (previous content could not be restored: %v)", err, rbErr), true)
		}
		return toolResult(fmt.Sprintf("Error: %v (the artifact was left unchanged)", err), true)
	}
	rep.Commit()
	return toolResult(fmt.Sprintf("HTML を更新しました（同じ URL のまま内容を差し替えました）。\nartifact_id: %s\nagmux 上の表示パス: %s\n開いている画面は自動で再読み込みされ、ユーザーにはブラウザ通知が送られました。再度更新するときも artifact_id を指定してください。", id, viewPath), false)
}

func (s *Server) apiCheckArtifact(id string) error {
	resp, err := http.Get(fmt.Sprintf("%s/api/artifacts/%s", s.apiURL, url.PathEscape(id)))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("artifact %s was not found (use the artifact_id returned by publish_artifact)", id)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (s *Server) apiUpdateArtifact(sessionID, id, title string) (string, error) {
	payload := map[string]string{"sessionId": sessionID}
	if title != "" {
		payload["title"] = title
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("PUT", fmt.Sprintf("%s/api/artifacts/%s", s.apiURL, url.PathEscape(id)), strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	return doArtifactRequest(req)
}

func (s *Server) apiRegisterArtifact(sessionID, id, title string) (string, error) {
	body, _ := json.Marshal(map[string]string{"id": id, "title": title})
	u := fmt.Sprintf("%s/api/sessions/%s/artifacts", s.apiURL, sessionID)
	req, err := http.NewRequest("POST", u, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	return doArtifactRequest(req)
}

func doArtifactRequest(req *http.Request) (string, error) {
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
