package server

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/myuon/agmux/internal/config"
	"github.com/myuon/agmux/internal/db"
	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/learning"
	"github.com/myuon/agmux/internal/session"
)

func (s *Server) initLearning() {
	if s.sqlDB == nil {
		return
	}
	streamsDir, err := db.StreamsDir()
	if err != nil {
		s.logger.Warn("learning: streams dir unavailable", "error", err)
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		s.logger.Warn("learning: home dir unavailable", "error", err)
		return
	}
	store, err := learning.NewStore(s.sqlDB)
	if err != nil {
		s.logger.Warn("learning: init store failed", "error", err)
		return
	}
	claudeCmd := "claude"
	if cfg, err := config.Load(); err == nil && strings.TrimSpace(cfg.Session.ClaudeCommand) != "" {
		claudeCmd = strings.Fields(cfg.Session.ClaudeCommand)[0]
	}
	r := learning.NewReviewer(store, s.sqlDB, streamsDir, home, claudeCmd, s.logger)
	r.Model = func() string {
		if cfg, err := config.Load(); err == nil && strings.TrimSpace(cfg.LearningInbox.Model) != "" {
			return strings.TrimSpace(cfg.LearningInbox.Model)
		}
		return learning.DefaultModel
	}
	r.StartSession = func(name, projectPath, prompt string) (string, error) {
		sess, err := s.sessions.Create(name, projectPath, prompt, true, session.CreateOpts{})
		if err != nil {
			return "", err
		}
		s.broadcastSessionUpdate()
		return sess.ID, nil
	}
	r.OnProposals = func(sessionID, sessionName string, n int) {
		msg := fmt.Sprintf("学びの受信箱に %d 件の提案があります", n)
		if err := s.saveNotification(sessionID, "notification", msg); err != nil {
			slog.Error("failed to save learning notification", "error", err)
		}
		s.hub.Broadcast(Message{
			Type: "agent_notification",
			Data: map[string]interface{}{
				"sessionId":   sessionID,
				"sessionName": sessionName,
				"message":     msg,
			},
		})
		s.hub.Broadcast(Message{Type: "learning_proposals", Data: map[string]interface{}{"sessionId": sessionID, "count": n}})
	}
	s.learning = r
}

func (s *Server) learningEnabled() bool {
	return s.learning != nil && experimental.Enabled(learning.FeatureName)
}

// maybeReviewLearning starts a background review when a session's goal stack
// has just been emptied.
func (s *Server) maybeReviewLearning(sessionID string) {
	if s.learningEnabled() {
		s.learning.ReviewAsync(sessionID)
	}
}

func (s *Server) requireLearning(w http.ResponseWriter) bool {
	if !s.learningEnabled() {
		writeError(w, http.StatusNotFound, "learning_inbox is disabled")
		return false
	}
	return true
}

func (s *Server) listLearningProposals(w http.ResponseWriter, r *http.Request) {
	if !s.requireLearning(w) {
		return
	}
	ps, err := s.learning.Store().List(r.URL.Query().Get("status"), 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"proposals": ps})
}

func proposalID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "pid"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid proposal id")
		return 0, false
	}
	return id, true
}

func (s *Server) acceptLearningProposal(w http.ResponseWriter, r *http.Request) {
	if !s.requireLearning(w) {
		return
	}
	id, ok := proposalID(w, r)
	if !ok {
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	p, err := s.learning.Accept(id, req.Content)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"proposal": p})
}

func (s *Server) rejectLearningProposal(w http.ResponseWriter, r *http.Request) {
	if !s.requireLearning(w) {
		return
	}
	id, ok := proposalID(w, r)
	if !ok {
		return
	}
	p, err := s.learning.Reject(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"proposal": p})
}

func (s *Server) reviewSessionLearning(w http.ResponseWriter, r *http.Request) {
	if !s.requireLearning(w) {
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := s.sessions.Get(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	started := s.learning.ReviewAsync(id)
	writeJSON(w, http.StatusAccepted, map[string]interface{}{"started": started})
}
