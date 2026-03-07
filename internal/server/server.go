package server

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/myuon/agmux/internal/config"
	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/logging"
	"github.com/myuon/agmux/internal/monitor"
	"github.com/myuon/agmux/internal/session"
)

type Server struct {
	sessions *session.Manager
	hub      *Hub
	router   chi.Router
	devMode  bool
	logPath  string
	logger   *slog.Logger
}

func New(sessions *session.Manager, hub *Hub, devMode bool, logPath string, logger *slog.Logger) *Server {
	s := &Server{
		sessions: sessions,
		hub:      hub,
		devMode:  devMode,
		logPath:  logPath,
		logger:   logger.With("component", "server"),
	}
	s.setupRoutes()
	return s
}

func (s *Server) setupRoutes() {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)

	if s.devMode {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   []string{"http://localhost:5173"},
			AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Content-Type"},
			AllowCredentials: true,
		}))
	}

	r.Get("/ws", s.hub.HandleWS)

	r.Route("/api", func(r chi.Router) {
		r.Get("/sessions", s.listSessions)
		r.Post("/sessions", s.createSession)
		r.Get("/sessions/{id}", s.getSession)
		r.Delete("/sessions/{id}", s.deleteSession)
		r.Post("/sessions/{id}/stop", s.stopSession)
		r.Post("/sessions/{id}/send", s.sendToSession)
		r.Put("/sessions/{id}/context", s.updateSessionContext)
		r.Post("/sessions/{id}/goals", s.createGoal)
		r.Post("/sessions/{id}/goals/complete", s.completeGoal)
		r.Post("/sessions/{id}/reconnect", s.reconnectSession)
		r.Post("/sessions/{id}/clear", s.clearSession)
		r.Get("/sessions/{id}/output", s.getSessionOutput)
		r.Get("/sessions/{id}/stream", s.getSessionStream)
		r.Get("/sessions/{id}/diff", s.getSessionDiff)
		r.Post("/sessions/controller/restart", s.restartController)
		r.Get("/logs", s.getLogs)
		r.Get("/config", s.getConfig)
		r.Put("/config", s.updateConfig)
	})

	s.router = r
}

func (s *Server) MountFrontend(frontendFS fs.FS) {
	fileServer := http.FileServer(http.FS(frontendFS))
	s.router.(*chi.Mux).Handle("/*", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Try to serve the file directly
		f, err := frontendFS.Open(r.URL.Path[1:]) // strip leading /
		if err != nil {
			// Fallback to index.html for SPA routing
			r.URL.Path = "/"
		} else {
			f.Close()
		}
		fileServer.ServeHTTP(w, r)
	}))
}

func (s *Server) Handler() http.Handler {
	return s.router
}

func (s *Server) NewHTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:    addr,
		Handler: s.router,
	}
}

// API handlers

type createSessionRequest struct {
	Name        string `json:"name"`
	ProjectPath string `json:"projectPath"`
	Prompt      string `json:"prompt,omitempty"`
	OutputMode  string `json:"outputMode,omitempty"`
	Worktree    bool   `json:"worktree,omitempty"`
}

type sendRequest struct {
	Text string `json:"text"`
}

type updateContextRequest struct {
	CurrentTask string `json:"currentTask"`
	Goal        string `json:"goal"`
}

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.sessions.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if sessions == nil {
		sessions = []session.Session{}
	}
	writeJSON(w, http.StatusOK, sessions)
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var req createSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.ProjectPath == "" {
		writeError(w, http.StatusBadRequest, "name and projectPath are required")
		return
	}
	sess, err := s.sessions.Create(req.Name, req.ProjectPath, req.Prompt, session.OutputMode(req.OutputMode), req.Worktree)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordSessionAction(sess.ID, "session_create", "name: "+req.Name+", path: "+req.ProjectPath)
	writeJSON(w, http.StatusCreated, sess)
}

type sessionResponse struct {
	*session.Session
	GithubURL  string      `json:"githubUrl,omitempty"`
	Branch     string      `json:"branch,omitempty"`
	PullRequests []prInfo  `json:"pullRequests,omitempty"`
}

type prInfo struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	State  string `json:"state"`
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess, err := s.sessions.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	resp := sessionResponse{Session: sess, GithubURL: detectGithubURL(sess.ProjectPath)}
	resp.Branch = detectBranch(sess.ProjectPath)
	if resp.Branch != "" {
		resp.PullRequests = detectPullRequests(sess.ProjectPath, resp.Branch)
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess, err := s.sessions.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if sess.Type == session.TypeController {
		writeError(w, http.StatusForbidden, "controller session cannot be deleted")
		return
	}
	if err := s.sessions.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordSessionAction(id, "session_delete", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) stopSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.sessions.Stop(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordSessionAction(id, "session_stop", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

func (s *Server) sendToSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req sendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.sessions.SendKeys(id, req.Text); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.sessions.UpdateStatus(id, session.StatusWorking)
	s.recordSessionAction(id, "session_send_keys", req.Text)
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
}

func (s *Server) updateSessionContext(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req updateContextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.sessions.UpdateContext(id, req.CurrentTask, req.Goal); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type createGoalRequest struct {
	CurrentTask string `json:"currentTask"`
	Goal        string `json:"goal"`
	Subgoal     bool   `json:"subgoal"`
}

func (s *Server) createGoal(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req createGoalRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.sessions.CreateGoal(id, req.CurrentTask, req.Goal, req.Subgoal); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) completeGoal(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	parent, err := s.sessions.CompleteGoal(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result := map[string]interface{}{"status": "ok"}
	if parent != nil {
		result["parentGoal"] = parent
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) reconnectSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.sessions.Reconnect(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordSessionAction(id, "session_reconnect", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "reconnected"})
}

func (s *Server) clearSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.sessions.Clear(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordSessionAction(id, "session_clear", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func (s *Server) getSessionOutput(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	output, err := s.sessions.CaptureOutput(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"output": output})
}

func (s *Server) getSessionStream(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	// Delta fetch: if "after" is specified, return only new lines since that index
	if q := r.URL.Query().Get("after"); q != "" {
		after, err := strconv.Atoi(q)
		if err != nil || after < 0 {
			writeError(w, http.StatusBadRequest, "invalid after parameter")
			return
		}
		lines, total, err := s.sessions.GetStreamLinesAfter(id, after)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		var result []json.RawMessage
		for _, line := range lines {
			result = append(result, json.RawMessage(line))
		}
		if result == nil {
			result = []json.RawMessage{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"lines": result,
			"total": total,
		})
		return
	}

	// Legacy: return last N lines (for initial load / backward compatibility)
	limit := 200
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}

	lines, total, err := s.sessions.GetStreamLines(id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var result []json.RawMessage
	for _, line := range lines {
		result = append(result, json.RawMessage(line))
	}
	if result == nil {
		result = []json.RawMessage{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"lines": result,
		"total": total,
	})
}

func (s *Server) getLogs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}

	if s.logPath == "" {
		writeJSON(w, http.StatusOK, []string{})
		return
	}

	file, err := os.Open(s.logPath)
	if err != nil {
		writeJSON(w, http.StatusOK, []string{})
		return
	}
	defer file.Close()

	// Read all lines, keep last N
	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}

	// Parse JSON lines into raw objects
	var logs []json.RawMessage
	for _, line := range lines {
		logs = append(logs, json.RawMessage(line))
	}
	if logs == nil {
		logs = []json.RawMessage{}
	}

	writeJSON(w, http.StatusOK, logs)
}

type configJSON struct {
	Server  configServerJSON  `json:"server"`
	Daemon  configDaemonJSON  `json:"daemon"`
	Session configSessionJSON `json:"session"`
	Prompts *configPromptsJSON `json:"prompts,omitempty"`
}

type configPromptsJSON struct {
	StatusCheck string `json:"statusCheck"`
}

type configServerJSON struct {
	Port int `json:"port"`
}
type configDaemonJSON struct {
	Interval string `json:"interval"`
}
type configSessionJSON struct {
	ClaudeCommand string `json:"claudeCommand"`
}

func configToJSON(cfg *config.Config) configJSON {
	return configJSON{
		Server:  configServerJSON{Port: cfg.Server.Port},
		Daemon:  configDaemonJSON{Interval: cfg.Daemon.Interval},
		Session: configSessionJSON{ClaudeCommand: cfg.Session.ClaudeCommand},
	}
}

func jsonToConfig(j configJSON) *config.Config {
	return &config.Config{
		Server:  config.ServerConfig{Port: j.Server.Port},
		Daemon:  config.DaemonConfig{Interval: j.Daemon.Interval},
		Session: config.SessionConfig{ClaudeCommand: j.Session.ClaudeCommand},
	}
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result := configToJSON(cfg)
	result.Prompts = &configPromptsJSON{
		StatusCheck: monitor.StatusPrompt,
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) updateConfig(w http.ResponseWriter, r *http.Request) {
	var req configJSON
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	cfg := jsonToConfig(req)
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) restartController(w http.ResponseWriter, r *http.Request) {
	controllerDir, err := db.ControllerDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sess, err := s.sessions.CreateController(controllerDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordSessionAction(sess.ID, "controller_restart", "")
	writeJSON(w, http.StatusOK, sess)
}

// helpers

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func detectGithubURL(projectPath string) string {
	cmd := exec.Command("git", "remote", "get-url", "origin")
	cmd.Dir = projectPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	remote := strings.TrimSpace(string(out))

	// SSH format: git@github.com:user/repo.git
	if strings.HasPrefix(remote, "git@github.com:") {
		path := strings.TrimPrefix(remote, "git@github.com:")
		path = strings.TrimSuffix(path, ".git")
		return "https://github.com/" + path
	}
	// HTTPS format: https://github.com/user/repo.git
	if strings.Contains(remote, "github.com") && strings.HasPrefix(remote, "https://") {
		return strings.TrimSuffix(remote, ".git")
	}
	// SSH format: ssh://git@github.com/user/repo.git
	if strings.HasPrefix(remote, "ssh://") && strings.Contains(remote, "github.com") {
		path := remote
		path = strings.TrimPrefix(path, "ssh://git@github.com/")
		path = strings.TrimPrefix(path, "ssh://github.com/")
		path = strings.TrimSuffix(path, ".git")
		return "https://github.com/" + path
	}
	return ""
}

func detectBranch(projectPath string) string {
	cmd := exec.Command("git", "branch", "--show-current")
	cmd.Dir = projectPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func detectPullRequests(projectPath, branch string) []prInfo {
	cmd := exec.Command("gh", "pr", "list", "--state", "all", "--head", branch, "--json", "number,title,url,state", "--limit", "10")
	cmd.Dir = projectPath
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var prs []struct {
		Number int    `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"url"`
		State  string `json:"state"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil
	}
	result := make([]prInfo, len(prs))
	for i, pr := range prs {
		result[i] = prInfo{Number: pr.Number, Title: pr.Title, URL: pr.URL, State: pr.State}
	}
	return result
}

type diffFileEntry struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Diff   string `json:"diff"`
}

func (s *Server) getSessionDiff(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess, err := s.sessions.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	files, err := getWorkingTreeDiff(sess.ProjectPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"files": files})
}

func getWorkingTreeDiff(projectPath string) ([]diffFileEntry, error) {
	// Get file list with status
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = projectPath
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	statusOutput := strings.TrimSpace(string(out))
	if statusOutput == "" {
		return []diffFileEntry{}, nil
	}

	// Get combined diff (staged + unstaged) against HEAD
	diffCmd := exec.Command("git", "diff", "HEAD")
	diffCmd.Dir = projectPath
	diffOut, err := diffCmd.Output()
	if err != nil {
		// If HEAD doesn't exist (new repo), try without HEAD
		diffCmd = exec.Command("git", "diff")
		diffCmd.Dir = projectPath
		diffOut, _ = diffCmd.Output()
	}

	// Parse per-file diffs
	diffMap := parseDiffPerFile(string(diffOut))

	// Parse status lines
	var files []diffFileEntry
	for _, line := range strings.Split(statusOutput, "\n") {
		if len(line) < 4 {
			continue
		}
		// git status --porcelain format: XY filename
		statusCode := strings.TrimSpace(line[:2])
		filePath := strings.TrimSpace(line[3:])
		// Handle renamed files: "R  old -> new"
		if idx := strings.Index(filePath, " -> "); idx >= 0 {
			filePath = filePath[idx+4:]
		}

		// Map status codes to single letter
		displayStatus := mapGitStatus(statusCode)

		files = append(files, diffFileEntry{
			Path:   filePath,
			Status: displayStatus,
			Diff:   diffMap[filePath],
		})
	}

	return files, nil
}

func mapGitStatus(code string) string {
	switch {
	case code == "??":
		return "?"
	case code == "!!":
		return "!"
	case strings.Contains(code, "A"):
		return "A"
	case strings.Contains(code, "D"):
		return "D"
	case strings.Contains(code, "R"):
		return "R"
	case strings.Contains(code, "M"):
		return "M"
	default:
		return code
	}
}

func parseDiffPerFile(diffOutput string) map[string]string {
	result := make(map[string]string)
	if diffOutput == "" {
		return result
	}

	// Split by "diff --git" boundaries
	parts := strings.Split(diffOutput, "diff --git ")
	for _, part := range parts[1:] { // skip first empty element
		fullDiff := "diff --git " + part

		// Extract filename from "diff --git a/path b/path"
		firstLine := strings.SplitN(part, "\n", 2)[0]
		tokens := strings.Fields(firstLine)
		if len(tokens) >= 2 {
			filePath := strings.TrimPrefix(tokens[1], "b/")
			result[filePath] = strings.TrimRight(fullDiff, "\n")
		}
	}

	return result
}

func (s *Server) recordSessionAction(sessionID, actionType, detail string) {
	logging.LogAction(s.logger, sessionID, actionType, detail, "user")

	s.hub.Broadcast(Message{
		Type: "action_log",
		Data: map[string]interface{}{
			"sessionId":  sessionID,
			"actionType": actionType,
			"detail":     detail,
			"timestamp":  time.Now(),
		},
	})
}
