package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
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
		// DataFiles: nil = not given (keep on update), {} = clear.
		DataFiles *map[string]string `json:"data_files"`
		// Pinned: nil = not given (keep on update, unpinned on create).
		Pinned *bool `json:"pinned"`
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
		return s.updateArtifact(sessionID, input.ArtifactID, input.Path, input.Title, input.DataFiles, input.Pinned), nil
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
	var dataFiles map[string]string
	if input.DataFiles != nil {
		dataFiles = *input.DataFiles
	}
	viewPath, err := s.apiRegisterArtifact(sessionID, id, input.Title, dataFiles, input.Pinned != nil && *input.Pinned)
	if err != nil {
		_ = artifact.RemoveFiles(dir, id)
		return toolResult(fmt.Sprintf("Error: %v", err), true), nil
	}
	return toolResult(fmt.Sprintf("HTML を公開しました。\nartifact_id: %s\nagmux 上の表示パス: %s\n公開時点の内容がコピーされています（元ファイルを後で変更しても反映されません）。内容を変更したいときは、元の HTML を編集してから artifact_id を指定して publish_artifact を再度呼んでください（同じ URL のまま差し替わります。~/.agmux/artifacts 配下は直接編集しないこと）。相対パスのアセット（画像・CSS・JS ファイル）は同梱されないため、HTML は自己完結（インライン、または https の CDN）にしてください。ユーザーにはブラウザ通知が送られました。%s", id, viewPath, dataFilesNote(id, dataFiles)), false), nil
}

// dataFilesNote describes how the artifact can fetch its registered data files.
func dataFilesNote(id string, files map[string]string) string {
	if len(files) == 0 {
		return ""
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	return fmt.Sprintf("\nデータファイル: %s\nHTML からは const res = await agmuxData(\"<name>\") で取得し、await res.text() / await res.json() で中身を読んでください（開くたびにディスク上の最新の内容が返ります）。agmuxData は agmux が HTML に自動で注入するヘルパーで、親ページ経由で取得するため Cloudflare Access 等の認証プロキシ越しでも動きます。直接の fetch(\"/artifacts/%s/data/<name>\") は認証プロキシ越しだと失敗します。", strings.Join(names, ", "), id)
}

// updateArtifact replaces an existing artifact's content under the same id.
// Order: check existence via the API (nothing written for unknown ids), swap
// the file atomically (keeping a backup), tell the server, and roll the file
// back if the server refuses so file and metadata stay consistent.
func (s *Server) updateArtifact(sessionID, id, path, title string, dataFiles *map[string]string, pinned *bool) interface{} {
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
	viewPath, err := s.apiUpdateArtifact(sessionID, id, title, dataFiles, pinned)
	if err != nil {
		if rbErr := rep.Rollback(); rbErr != nil {
			return toolResult(fmt.Sprintf("Error: %v (previous content could not be restored: %v)", err, rbErr), true)
		}
		return toolResult(fmt.Sprintf("Error: %v (the artifact was left unchanged)", err), true)
	}
	rep.Commit()
	note := ""
	if dataFiles != nil {
		if len(*dataFiles) == 0 {
			note = "\nデータファイルの登録を解除しました。"
		} else {
			note = dataFilesNote(id, *dataFiles)
		}
	}
	return toolResult(fmt.Sprintf("HTML を更新しました（同じ URL のまま内容を差し替えました）。\nartifact_id: %s\nagmux 上の表示パス: %s\n開いている画面は自動で再読み込みされ、ユーザーにはブラウザ通知が送られました。再度更新するときも artifact_id を指定してください。%s", id, viewPath, note), false)
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

func (s *Server) apiUpdateArtifact(sessionID, id, title string, dataFiles *map[string]string, pinned *bool) (string, error) {
	payload := map[string]interface{}{"sessionId": sessionID}
	if title != "" {
		payload["title"] = title
	}
	if dataFiles != nil {
		m := *dataFiles
		if m == nil {
			m = map[string]string{}
		}
		payload["dataFiles"] = m
	}
	if pinned != nil {
		payload["pinned"] = *pinned
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest("PUT", fmt.Sprintf("%s/api/artifacts/%s", s.apiURL, url.PathEscape(id)), strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	return doArtifactRequest(req)
}

func (s *Server) apiRegisterArtifact(sessionID, id, title string, dataFiles map[string]string, pinned bool) (string, error) {
	payload := map[string]interface{}{"id": id, "title": title}
	if len(dataFiles) > 0 {
		payload["dataFiles"] = dataFiles
	}
	if pinned {
		payload["pinned"] = true
	}
	body, _ := json.Marshal(payload)
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
