package server

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"log"
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
		r.Get("/sessions/{id}/output", s.getSessionOutput)
		r.Get("/sessions/{id}/stream", s.getSessionStream)
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

func (s *Server) ListenAndServe(addr string) error {
	log.Printf("Server listening on %s", addr)
	return http.ListenAndServe(addr, s.router)
}

// API handlers

type createSessionRequest struct {
	Name        string `json:"name"`
	ProjectPath string `json:"projectPath"`
	Prompt      string `json:"prompt,omitempty"`
	OutputMode  string `json:"outputMode,omitempty"`
}

type sendRequest struct {
	Text string `json:"text"`
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
	sess, err := s.sessions.Create(req.Name, req.ProjectPath, req.Prompt, session.OutputMode(req.OutputMode))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordSessionAction(sess.ID, "session_create", "name: "+req.Name+", path: "+req.ProjectPath)
	writeJSON(w, http.StatusCreated, sess)
}

type sessionResponse struct {
	*session.Session
	GithubURL string `json:"githubUrl,omitempty"`
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess, err := s.sessions.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	resp := sessionResponse{Session: sess, GithubURL: detectGithubURL(sess.ProjectPath)}
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
	s.recordSessionAction(id, "session_send_keys", req.Text)
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent"})
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

	limit := 200
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			limit = n
		}
	}

	lines, err := s.sessions.GetStreamLines(id, limit)
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

	writeJSON(w, http.StatusOK, result)
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
	writeJSON(w, http.StatusOK, configToJSON(cfg))
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
