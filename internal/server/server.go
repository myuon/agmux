package server

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/gorilla/websocket"
	"github.com/myuon/agmux/internal/config"
	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/logging"
	"github.com/myuon/agmux/internal/monitor"
	"github.com/myuon/agmux/internal/otel"
	"github.com/myuon/agmux/internal/session"
)

type Server struct {
	sessions         session.SessionService
	hub              *Hub
	router           chi.Router
	devMode          bool
	logPath          string
	logger           *slog.Logger
	escalations      *EscalationStore
	otelReceiver     *otel.Receiver
	sqlDB            *sql.DB
	externalDetector *session.ExternalDetector
}

func New(sessions session.SessionService, hub *Hub, devMode bool, logPath string, logger *slog.Logger, sqlDB *sql.DB) *Server {
	extDetector := session.NewExternalDetector(logger, 10*time.Second)
	go extDetector.Start()

	s := &Server{
		sessions:         sessions,
		hub:              hub,
		devMode:          devMode,
		logPath:          logPath,
		logger:           logger.With("component", "server"),
		escalations:      NewEscalationStore(),
		otelReceiver:     otel.NewReceiver(sqlDB, logger),
		sqlDB:            sqlDB,
		externalDetector: extDetector,
	}

	// Wire real-time stream updates via WebSocket
	sessions.SetOnNewLines(func(sessionID string, newLines []string, total int) {
		// Convert lines to json.RawMessage for proper JSON encoding
		rawLines := make([]json.RawMessage, len(newLines))
		for i, line := range newLines {
			rawLines[i] = json.RawMessage(line)
		}
		hub.Broadcast(Message{
			Type: "stream_update",
			Data: map[string]interface{}{
				"sessionId": sessionID,
				"lines":     rawLines,
				"total":     total,
			},
		})
	})

	s.setupRoutes()
	return s
}

// ExternalDetector returns the server's external process detector.
func (s *Server) ExternalDetector() *session.ExternalDetector {
	return s.externalDetector
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

	// OTLP receiver endpoints
	r.Post("/v1/metrics", s.otelReceiver.HandleMetrics)
	r.Post("/v1/logs", s.otelReceiver.HandleLogs)

	r.Route("/api", func(r chi.Router) {
		r.Get("/sessions", s.listSessions)
		r.Post("/sessions", s.createSession)
		r.Get("/sessions/{id}", s.getSession)
		r.Delete("/sessions/{id}", s.deleteSession)
		r.Post("/sessions/{id}/stop", s.stopSession)
		r.Post("/sessions/{id}/send", s.sendToSession)
		r.Put("/sessions/{id}/context", s.updateSessionContext)
		r.Get("/sessions/{id}/goals", s.getGoals)
		r.Post("/sessions/{id}/goals", s.createGoal)
		r.Post("/sessions/{id}/goals/complete", s.completeGoal)
		r.Post("/sessions/{id}/duplicate", s.duplicateSession)
		r.Post("/sessions/{id}/reconnect", s.reconnectSession)
		r.Post("/sessions/{id}/clear", s.clearSession)
		r.Get("/sessions/{id}/stream", s.getSessionStream)
		r.Get("/sessions/{id}/diff", s.getSessionDiff)
		r.Get("/sessions/{id}/claude-md", s.getClaudeMD)
		r.Get("/sessions/{id}/settings-json", s.getSettingsJSON)
		r.Get("/sessions/{id}/escalate", s.getPendingEscalation)
		r.Post("/sessions/{id}/escalate", s.createEscalation)
		r.Post("/sessions/{id}/escalate/respond", s.respondEscalation)
		r.Post("/sessions/{id}/notify", s.sendNotification)
		r.Post("/sessions/controller/restart", s.restartController)
		r.Get("/claude/models", s.getClaudeModels)
		r.Get("/claude/version", s.getClaudeVersion)
		r.Get("/logs", s.getLogs)
		r.Get("/config", s.getConfig)
		r.Put("/config", s.updateConfig)
		r.Get("/codex/models", s.getCodexModels)
		r.Get("/codex/version", s.getCodexVersion)
		r.Get("/metrics", s.getMetrics)
		r.Get("/metrics/summary", s.getMetricsSummary)
		r.Get("/metrics/events", s.getMetricsEvents)
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
	Worktree    bool   `json:"worktree,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	AutoApprove bool   `json:"autoApprove,omitempty"`
}

type sendImageData struct {
	Data      string `json:"data"`
	MediaType string `json:"mediaType"`
}

type sendRequest struct {
	Text   string          `json:"text"`
	Images []sendImageData `json:"images,omitempty"`
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

	// Merge external (non-agmux) Claude sessions
	if s.externalDetector != nil {
		external := s.externalDetector.Sessions()
		sessions = append(sessions, external...)
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
	sess, err := s.sessions.Create(req.Name, req.ProjectPath, req.Prompt, req.Worktree, session.CreateOpts{Provider: session.ProviderName(req.Provider), Model: req.Model, FullAuto: req.AutoApprove})
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

func (s *Server) duplicateSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, err := s.sessions.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if existing.Type == session.TypeController {
		writeError(w, http.StatusForbidden, "controller session cannot be duplicated")
		return
	}
	sess, err := s.sessions.Duplicate(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordSessionAction(sess.ID, "session_duplicate", "duplicated from "+id)
	writeJSON(w, http.StatusCreated, sess)
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
	// Convert images if present
	var images []session.ImageData
	for _, img := range req.Images {
		images = append(images, session.ImageData{
			Data:      img.Data,
			MediaType: img.MediaType,
		})
	}
	if err := s.sessions.SendKeysWithImages(id, req.Text, images); err != nil {
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

func (s *Server) getGoals(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess, err := s.sessions.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"currentTask": sess.CurrentTask,
		"goal":        sess.Goal,
		"goals":       sess.Goals,
	})
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
	cgResult, err := s.sessions.CompleteGoal(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	result := map[string]interface{}{"status": "ok"}
	if cgResult.ParentGoal != nil {
		result["parentGoal"] = cgResult.ParentGoal
	}

	// Broadcast goal_completed notification via WebSocket
	if cgResult.CompletedGoal != nil && !cgResult.CompletedGoal.StartedAt.IsZero() {
		durationMs := time.Since(cgResult.CompletedGoal.StartedAt).Milliseconds()
		sess, _ := s.sessions.Get(id)
		sessionName := ""
		if sess != nil {
			sessionName = sess.Name
		}
		s.hub.Broadcast(Message{
			Type: "goal_completed",
			Data: map[string]interface{}{
				"sessionId":   id,
				"sessionName": sessionName,
				"currentTask": cgResult.CompletedGoal.CurrentTask,
				"goal":        cgResult.CompletedGoal.Goal,
				"durationMs":  durationMs,
			},
		})
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getPendingEscalation(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	esc := s.escalations.GetBySession(sessionID)
	if esc == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"escalation": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"escalation": esc})
}

type createEscalationRequest struct {
	ID             string `json:"id"`
	Message        string `json:"message"`
	TimeoutSeconds *int   `json:"timeout_seconds,omitempty"`
}

func (s *Server) createEscalation(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	var req createEscalationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Message == "" || req.ID == "" {
		writeError(w, http.StatusBadRequest, "id and message are required")
		return
	}

	// Get session name for notification
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	s.recordSessionAction(sessionID, "escalation", req.Message)

	// Determine timeout duration
	timeoutSeconds := 300 // default 5 minutes
	if req.TimeoutSeconds != nil && *req.TimeoutSeconds > 0 {
		timeoutSeconds = *req.TimeoutSeconds
	}

	// Create escalation and get response channel
	ch := s.escalations.Create(req.ID, sessionID, req.Message, timeoutSeconds)

	// Broadcast WebSocket notification (after timeout is determined)
	s.hub.Broadcast(Message{
		Type: "escalation",
		Data: map[string]interface{}{
			"id":             req.ID,
			"sessionId":      sessionID,
			"sessionName":    sess.Name,
			"message":        req.Message,
			"timeoutSeconds": timeoutSeconds,
		},
	})
	timeout := time.Duration(timeoutSeconds) * time.Second

	// Block until user responds (with timeout)
	select {
	case response := <-ch:
		s.escalations.Cleanup(req.ID)
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":    "responded",
			"response":  response,
			"timed_out": false,
		})
	case <-time.After(timeout):
		autoResponse := "ユーザーが未応答のため、あなたの判断で進めてください。判断が却下される可能性があるので、リバート可能な形（コミットを細かく打つなど）で作業を進めてください。"
		s.escalations.MarkTimedOut(req.ID, autoResponse)
		// Broadcast timeout notification
		s.hub.Broadcast(Message{
			Type: "escalation_timeout",
			Data: map[string]interface{}{
				"id":        req.ID,
				"sessionId": sessionID,
			},
		})
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":    "timed_out",
			"response":  autoResponse,
			"timed_out": true,
		})
	case <-r.Context().Done():
		s.escalations.Cleanup(req.ID)
		writeError(w, http.StatusGatewayTimeout, "request cancelled")
	}
}

type sendNotificationRequest struct {
	Message string `json:"message"`
}

func (s *Server) sendNotification(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")
	var req sendNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message is required")
		return
	}

	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	s.recordSessionAction(sessionID, "agent_notification", req.Message)

	// Broadcast WebSocket notification to all connected clients
	s.hub.Broadcast(Message{
		Type: "agent_notification",
		Data: map[string]interface{}{
			"sessionId":   sessionID,
			"sessionName": sess.Name,
			"message":     req.Message,
		},
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type respondEscalationRequest struct {
	ID       string `json:"id"`
	Response string `json:"response"`
}

func (s *Server) respondEscalation(w http.ResponseWriter, r *http.Request) {
	var req respondEscalationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ID == "" || req.Response == "" {
		writeError(w, http.StatusBadRequest, "id and response are required")
		return
	}

	if !s.escalations.Respond(req.ID, req.Response) {
		writeError(w, http.StatusNotFound, "escalation not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
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

	// Parse JSON lines into raw objects, skipping invalid JSON
	var logs []json.RawMessage
	for _, line := range lines {
		raw := json.RawMessage(line)
		if json.Valid(raw) {
			logs = append(logs, raw)
		}
	}
	if logs == nil {
		logs = []json.RawMessage{}
	}

	writeJSON(w, http.StatusOK, logs)
}

type configJSON struct {
	Server  configServerJSON   `json:"server"`
	Daemon  configDaemonJSON   `json:"daemon"`
	Session configSessionJSON  `json:"session"`
	Claude  configClaudeJSON   `json:"claude"`
	Prompts *configPromptsJSON `json:"prompts,omitempty"`
}

type configPromptsJSON struct {
	StatusCheck  string `json:"statusCheck"`
	SystemPrompt string `json:"systemPrompt"`
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
type configClaudeJSON struct {
	PermissionMode string `json:"permissionMode"`
}

func configToJSON(cfg *config.Config) configJSON {
	return configJSON{
		Server:  configServerJSON{Port: cfg.Server.Port},
		Daemon:  configDaemonJSON{Interval: cfg.Daemon.Interval},
		Session: configSessionJSON{ClaudeCommand: cfg.Session.ClaudeCommand},
		Claude:  configClaudeJSON{PermissionMode: cfg.Claude.ClaudePermissionMode()},
	}
}

func jsonToConfig(j configJSON) *config.Config {
	return &config.Config{
		Server:  config.ServerConfig{Port: j.Server.Port},
		Daemon:  config.DaemonConfig{Interval: j.Daemon.Interval},
		Session: config.SessionConfig{ClaudeCommand: j.Session.ClaudeCommand},
		Claude:  config.ClaudeConfig{PermissionMode: j.Claude.PermissionMode},
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
		StatusCheck:  monitor.StatusPrompt,
		SystemPrompt: s.sessions.SystemPrompt(),
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

func (s *Server) getCodexModels(w http.ResponseWriter, r *http.Request) {
	models, err := fetchCodexModels()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

// codexModel represents a model returned by the Codex app-server model/list method.
type codexModel struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	IsDefault       bool   `json:"isDefault,omitempty"`
	ReasoningEffort string `json:"reasoningEffort,omitempty"`
}

// fetchCodexModels starts a codex app-server process in WebSocket mode and
// performs the JSON-RPC handshake to retrieve the model list.
// The stdio transport does not work reliably with codex-cli 0.111.0+, so we
// use the WebSocket transport instead.
func fetchCodexModels() ([]codexModel, error) {
	// Find a free port for the WebSocket server
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("find free port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	listenAddr := fmt.Sprintf("ws://127.0.0.1:%d", port)
	cmd := exec.Command("codex", "app-server", "--listen", listenAddr)
	cmd.Stderr = nil
	cmd.Stdout = nil

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}

	// Ensure process is cleaned up
	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	// Wait for the WebSocket server to be ready
	wsURL := fmt.Sprintf("ws://127.0.0.1:%d", port)
	var conn *websocket.Conn
	for i := 0; i < 30; i++ {
		time.Sleep(100 * time.Millisecond)
		c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err == nil {
			conn = c
			break
		}
	}
	if conn == nil {
		return nil, fmt.Errorf("could not connect to codex app-server WebSocket on port %d", port)
	}
	defer conn.Close()

	// Send initialize request
	initReq := map[string]interface{}{
		"method": "initialize",
		"id":     1,
		"params": map[string]interface{}{
			"clientInfo": map[string]interface{}{
				"name":    "agmux",
				"version": "0.1",
			},
		},
	}
	if err := conn.WriteJSON(initReq); err != nil {
		return nil, fmt.Errorf("send initialize: %w", err)
	}

	// Read initialize response
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, _, err = conn.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read initialize response: %w", err)
	}

	// Send initialized notification
	if err := conn.WriteJSON(map[string]interface{}{"method": "initialized"}); err != nil {
		return nil, fmt.Errorf("send initialized: %w", err)
	}

	// Send model/list request
	if err := conn.WriteJSON(map[string]interface{}{"method": "model/list", "id": 2, "params": map[string]interface{}{}}); err != nil {
		return nil, fmt.Errorf("send model/list: %w", err)
	}

	// Read responses until we get the model/list response (id: 2)
	for i := 0; i < 20; i++ {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("read model/list response: %w", err)
		}
		var resp struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(msg, &resp) == nil && resp.ID == 2 && resp.Result != nil {
			// Parse the result which contains a data array
			var result struct {
				Data []struct {
					ID                     string `json:"id"`
					DisplayName            string `json:"displayName"`
					Description            string `json:"description"`
					IsDefault              bool   `json:"isDefault"`
					Hidden                 bool   `json:"hidden"`
					DefaultReasoningEffort string `json:"defaultReasoningEffort"`
				} `json:"data"`
			}
			if err := json.Unmarshal(resp.Result, &result); err != nil {
				return nil, fmt.Errorf("parse model/list result: %w", err)
			}
			var models []codexModel
			for _, m := range result.Data {
				if m.Hidden {
					continue
				}
				models = append(models, codexModel{
					ID:              m.ID,
					Name:            m.DisplayName,
					Description:     m.Description,
					IsDefault:       m.IsDefault,
					ReasoningEffort: m.DefaultReasoningEffort,
				})
			}
			if models == nil {
				models = []codexModel{}
			}
			return models, nil
		}
	}

	return nil, fmt.Errorf("no model/list response received from codex app-server")
}

func (s *Server) getMetrics(w http.ResponseWriter, r *http.Request) {
	store := otel.NewStore(s.sqlDB)
	name := r.URL.Query().Get("name")
	sessionID := r.URL.Query().Get("session_id")
	var since time.Time
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			since = t
		}
	}
	metrics, err := store.QueryMetrics(name, sessionID, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if metrics == nil {
		metrics = []otel.MetricRow{}
	}
	writeJSON(w, http.StatusOK, metrics)
}

func (s *Server) getMetricsSummary(w http.ResponseWriter, r *http.Request) {
	store := otel.NewStore(s.sqlDB)
	var since time.Time
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			since = t
		}
	}
	summary, err := store.GetSummary(since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) getMetricsEvents(w http.ResponseWriter, r *http.Request) {
	store := otel.NewStore(s.sqlDB)
	name := r.URL.Query().Get("name")
	sessionID := r.URL.Query().Get("session_id")
	var since time.Time
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if t, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			since = t
		}
	}
	events, err := store.QueryEvents(name, sessionID, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if events == nil {
		events = []otel.EventRow{}
	}
	writeJSON(w, http.StatusOK, events)
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

// getClaudeModels returns a hardcoded list of available Claude models.
func (s *Server) getClaudeModels(w http.ResponseWriter, r *http.Request) {
	type modelInfo struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Default bool   `json:"default,omitempty"`
	}
	models := []modelInfo{
		{ID: "claude-sonnet-4-5", Name: "Claude Sonnet 4.5", Default: true},
		{ID: "claude-opus-4-6", Name: "Claude Opus 4.6"},
		{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6"},
		{ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5"},
	}
	writeJSON(w, http.StatusOK, models)
}

// getClaudeVersion runs "claude --version" and returns the parsed version string.
func (s *Server) getClaudeVersion(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("claude", "--version")
	out, err := cmd.Output()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get claude version: "+err.Error())
		return
	}
	version := strings.TrimSpace(string(out))
	writeJSON(w, http.StatusOK, map[string]string{"version": version})
}

// getCodexVersion runs "codex --version" and returns the parsed version string.
func (s *Server) getCodexVersion(w http.ResponseWriter, r *http.Request) {
	cmd := exec.Command("codex", "--version")
	out, err := cmd.Output()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get codex version: "+err.Error())
		return
	}
	version := strings.TrimSpace(string(out))
	writeJSON(w, http.StatusOK, map[string]string{"version": version})
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

// parseAtReferences extracts file paths from @filename references in CLAUDE.md content.
// It looks for lines starting with @ followed by a filename (e.g., "@RTK.md").
func parseAtReferences(content string, baseDir string) []string {
	var refs []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "@") && len(line) > 1 {
			ref := strings.TrimPrefix(line, "@")
			// Remove any trailing whitespace or comments
			ref = strings.Fields(ref)[0]
			refPath := filepath.Join(baseDir, ref)
			if _, err := os.Stat(refPath); err == nil {
				refs = append(refs, refPath)
			}
		}
	}
	return refs
}

type claudeMDFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (s *Server) getClaudeMD(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess, err := s.sessions.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	var files []claudeMDFile

	// Candidate paths for CLAUDE.md files
	candidates := []string{
		filepath.Join(sess.ProjectPath, "CLAUDE.md"),
		filepath.Join(sess.ProjectPath, ".claude", "CLAUDE.md"),
	}

	seen := map[string]bool{}

	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		content, err := os.ReadFile(candidate)
		if err != nil {
			continue
		}
		seen[candidate] = true

		// Compute relative path from project root for display
		relPath, _ := filepath.Rel(sess.ProjectPath, candidate)
		files = append(files, claudeMDFile{Path: relPath, Content: string(content)})

		// Parse @references and include referenced files
		refs := parseAtReferences(string(content), filepath.Dir(candidate))
		for _, ref := range refs {
			if seen[ref] {
				continue
			}
			seen[ref] = true
			refContent, err := os.ReadFile(ref)
			if err != nil {
				continue
			}
			refRelPath, _ := filepath.Rel(sess.ProjectPath, ref)
			files = append(files, claudeMDFile{Path: refRelPath, Content: string(refContent)})
		}
	}

	if len(files) == 0 {
		writeError(w, http.StatusNotFound, "CLAUDE.md not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"files": files})
}

func (s *Server) getSettingsJSON(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess, err := s.sessions.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	type settingsFile struct {
		Name    string `json:"name"`
		Content string `json:"content,omitempty"`
	}

	files := []settingsFile{}
	for _, name := range []string{"settings.json", "settings.local.json"} {
		p := filepath.Join(sess.ProjectPath, ".claude", name)
		content, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var parsed json.RawMessage
		if err := json.Unmarshal(content, &parsed); err != nil {
			continue
		}
		files = append(files, settingsFile{Name: name, Content: string(content)})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"files": files,
	})
}

func getWorkingTreeDiff(projectPath string) ([]diffFileEntry, error) {
	// Get file list with status
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = projectPath
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	statusOutput := strings.TrimRight(string(out), "\n")
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
		// git status --porcelain format: XY filename (XY is 2 chars, then 1 space)
		statusCode := strings.TrimSpace(line[:2])
		filePath := line[3:]
		// Handle renamed files: "R  old -> new"
		if idx := strings.Index(filePath, " -> "); idx >= 0 {
			filePath = filePath[idx+4:]
		}

		// Map status codes to single letter
		displayStatus := mapGitStatus(statusCode)

		diff := diffMap[filePath]

		// For untracked/new files not in diffMap, generate diff
		if diff == "" && (statusCode == "??" || strings.Contains(statusCode, "A")) {
			untrackedDiff, err := generateNewFileDiff(projectPath, filePath)
			if err == nil {
				diff = untrackedDiff
			}
		}

		files = append(files, diffFileEntry{
			Path:   filePath,
			Status: displayStatus,
			Diff:   diff,
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

func generateNewFileDiff(projectPath, filePath string) (string, error) {
	// git diff --no-index returns exit code 1 when files differ, which is expected.
	// Output() still returns stdout content even with non-zero exit code.
	cmd := exec.Command("git", "diff", "--no-index", "--", "/dev/null", filePath)
	cmd.Dir = projectPath
	out, _ := cmd.Output()
	if len(out) == 0 {
		return "", nil
	}
	return strings.TrimRight(string(out), "\n"), nil
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
