package server

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
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
		r.Get("/sessions/{id}/actions", s.getSessionActions)
		r.Get("/sessions/{id}/logs", s.getSessionLogs)
		r.Post("/sessions/controller/restart", s.restartController)
		r.Get("/actions", s.getActions)
		r.Get("/logs", s.getLogs)
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
	sess, err := s.sessions.Create(req.Name, req.ProjectPath, req.Prompt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.recordSessionAction(sess.ID, "session_create", "name: "+req.Name+", path: "+req.ProjectPath)
	writeJSON(w, http.StatusCreated, sess)
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	sess, err := s.sessions.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sess)
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

type daemonAction struct {
	SessionID          string `json:"sessionId"`
	ActionType         string `json:"actionType"`
	Detail             string `json:"detail"`
	Source             string `json:"source"`
	CapturedOutputTail string `json:"capturedOutputTail,omitempty"`
	PreviousStatus     string `json:"previousStatus,omitempty"`
	NewStatus          string `json:"newStatus,omitempty"`
	CreatedAt          string `json:"createdAt"`
}

// readActionsFromLog reads action entries from the log file, optionally filtered by sessionID.
func (s *Server) readActionsFromLog(sessionID string, limit int) []daemonAction {
	if s.logPath == "" {
		return []daemonAction{}
	}

	file, err := os.Open(s.logPath)
	if err != nil {
		return []daemonAction{}
	}
	defer file.Close()

	var actions []daemonAction
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(scanner.Text()), &entry); err != nil {
			continue
		}
		cat, _ := entry["category"].(string)
		if cat != "action" {
			continue
		}
		sid, _ := entry["sessionId"].(string)
		if sessionID != "" && sid != sessionID {
			continue
		}
		a := daemonAction{
			SessionID:          sid,
			ActionType:         strVal(entry, "actionType"),
			Detail:             strVal(entry, "detail"),
			Source:             strVal(entry, "source"),
			CapturedOutputTail: strVal(entry, "capturedOutputTail"),
			PreviousStatus:     strVal(entry, "previousStatus"),
			NewStatus:          strVal(entry, "newStatus"),
			CreatedAt:          strVal(entry, "time"),
		}
		actions = append(actions, a)
	}

	// Return last N entries (newest last in file, we want newest first)
	if len(actions) > limit {
		actions = actions[len(actions)-limit:]
	}
	// Reverse to newest-first
	for i, j := 0, len(actions)-1; i < j; i, j = i+1, j-1 {
		actions[i], actions[j] = actions[j], actions[i]
	}
	if actions == nil {
		actions = []daemonAction{}
	}
	return actions
}

func strVal(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

func (s *Server) getActions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.readActionsFromLog("", 50))
}

func (s *Server) getSessionActions(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	writeJSON(w, http.StatusOK, s.readActionsFromLog(id, 50))
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

type claudeContentBlock struct {
	Type    string `json:"type"`              // "text", "tool_use", "tool_result"
	Text    string `json:"text,omitempty"`    // for type=text
	Name    string `json:"name,omitempty"`    // for type=tool_use (tool name)
	Input   any    `json:"input,omitempty"`   // for type=tool_use (tool input)
	Content string `json:"content,omitempty"` // for type=tool_result
}

type claudeLogEntry struct {
	Type      string              `json:"type"`
	Timestamp string              `json:"timestamp"`
	Blocks    []claudeContentBlock `json:"blocks"`
}

func (s *Server) getSessionLogs(w http.ResponseWriter, r *http.Request) {
	sessionID := chi.URLParam(r, "id")

	// Get session to find projectPath
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	// Build Claude Code JSONL path: ~/.claude/projects/[escaped-path]/[sessionId].jsonl
	homeDir, err := os.UserHomeDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "cannot determine home directory")
		return
	}

	escapedPath := strings.ReplaceAll(sess.ProjectPath, "/", "-")
	escapedPath = strings.ReplaceAll(escapedPath, ".", "-")
	jsonlPath := filepath.Join(homeDir, ".claude", "projects", escapedPath, sessionID+".jsonl")

	file, err := os.Open(jsonlPath)
	if err != nil {
		writeJSON(w, http.StatusOK, []claudeLogEntry{})
		return
	}
	defer file.Close()

	var logs []claudeLogEntry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		var entry struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type != "user" && entry.Type != "assistant" {
			continue
		}

		blocks := extractContentBlocks(entry.Message.Content)
		if len(blocks) == 0 {
			continue
		}

		logs = append(logs, claudeLogEntry{
			Type:      entry.Type,
			Timestamp: entry.Timestamp,
			Blocks:    blocks,
		})
	}

	if logs == nil {
		logs = []claudeLogEntry{}
	}

	writeJSON(w, http.StatusOK, logs)
}

// extractContentBlocks parses Claude message content into typed blocks.
func extractContentBlocks(raw json.RawMessage) []claudeContentBlock {
	if len(raw) == 0 {
		return nil
	}

	// Try as string first (plain user message)
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return nil
		}
		return []claudeContentBlock{{Type: "text", Text: s}}
	}

	// Try as array of content blocks
	var rawBlocks []struct {
		Type      string          `json:"type"`
		Text      string          `json:"text,omitempty"`
		Name      string          `json:"name,omitempty"`
		Input     json.RawMessage `json:"input,omitempty"`
		Content   json.RawMessage `json:"content,omitempty"`
		ToolUseID string          `json:"tool_use_id,omitempty"`
	}
	if err := json.Unmarshal(raw, &rawBlocks); err != nil {
		return nil
	}

	var blocks []claudeContentBlock
	for _, b := range rawBlocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				blocks = append(blocks, claudeContentBlock{Type: "text", Text: b.Text})
			}
		case "tool_use":
			var input any
			json.Unmarshal(b.Input, &input)
			blocks = append(blocks, claudeContentBlock{Type: "tool_use", Name: b.Name, Input: input})
		case "tool_result":
			content := ""
			// tool_result content can be string or structured
			json.Unmarshal(b.Content, &content)
			if content == "" {
				content = string(b.Content)
			}
			blocks = append(blocks, claudeContentBlock{Type: "tool_result", Content: content})
		}
	}
	return blocks
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
