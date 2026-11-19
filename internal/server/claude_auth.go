package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/myuon/agmux/internal/claudeauth"
	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/session"
)

// Claude re-login (experimental "claude_relogin", #832). The login URL and the
// code pass through these handlers; they must never be logged.

// claudeResumeMessage is sent to a session that stopped on the lost login when
// the user presses "再開".
const claudeResumeMessage = "Claude のログインが切れていたため中断していました。再ログインしたので、中断したところから続けてください。"

type claudeAuth struct {
	monitor *claudeauth.Monitor
	flow    *claudeauth.LoginFlow
	token   *claudeauth.LoginFlow // `claude setup-token` (claude_token.go)
	now     func() time.Time      // for tests; nil = time.Now
}

// InitClaudeAuth sets up login-loss detection and the re-login flow using the
// given claude command, and returns the monitor so the session manager and
// the automation scheduler can be wired to it.
func (s *Server) InitClaudeAuth(claudeCommand string) *claudeauth.Monitor {
	return s.initClaudeAuth(claudeauth.CLIStarter(claudeCommand), claudeauth.CLIStatusChecker(claudeCommand),
		claudeauth.CLISetupTokenStarter(claudeCommand))
}

func (s *Server) initClaudeAuth(start claudeauth.Starter, check claudeauth.StatusChecker, tokenStart claudeauth.Starter) *claudeauth.Monitor {
	mon := claudeauth.NewMonitor(func() bool { return experimental.Enabled(claudeauth.FeatureName) })
	flow := claudeauth.NewLoginFlow(start, check, func() {
		s.logger.Info("claude re-login completed")
		mon.Clear()
	})
	mon.SetOnChange(func(st claudeauth.State, becameRequired bool) {
		s.onClaudeAuthChange(st, becameRequired)
	})
	token := claudeauth.NewTokenFlow(tokenStart, s.saveClaudeToken, func() {
		// Sessions started from now on use the token.
		mon.Clear()
		s.broadcastClaudeAuth()
	})
	s.claudeAuth = &claudeAuth{monitor: mon, flow: flow, token: token}
	return mon
}

// ReportClaudeLoginRequired is the session manager hook.
func (s *Server) ReportClaudeLoginRequired(sessionID, reason string) {
	if s.claudeAuth != nil {
		s.claudeAuth.monitor.Report(sessionID, reason)
	}
}

func (s *Server) onClaudeAuthChange(st claudeauth.State, becameRequired bool) {
	if becameRequired {
		s.logger.Warn("claude login required; automations are paused until re-login")
		sessionID := ""
		if len(st.StoppedSessions) > 0 {
			sessionID = st.StoppedSessions[0]
		}
		msg := "Claude のログインが切れています。画面上部の［再ログイン］からログインし直してください。"
		if s.sqlDB != nil {
			if err := s.saveNotification(sessionID, "notification", msg); err != nil {
				s.logger.Warn("failed to save claude login notification", "error", err)
			}
		}
		if s.hub != nil {
			s.hub.Broadcast(Message{Type: "agent_notification", Data: map[string]interface{}{
				"sessionId":   sessionID,
				"sessionName": "Claude",
				"message":     msg,
			}})
		}
	}
	if s.hub != nil {
		s.hub.Broadcast(Message{Type: "claude_auth", Data: s.claudeAuthStatus()})
	}
}

type claudeAuthSession struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type claudeAuthStatusResponse struct {
	LoginRequired   bool                   `json:"loginRequired"`
	Since           *time.Time             `json:"since,omitempty"`
	Reason          string                 `json:"reason,omitempty"`
	StoppedSessions []claudeAuthSession    `json:"stoppedSessions"`
	Login           claudeauth.LoginStatus `json:"login"`
	Token           claudeTokenInfo        `json:"token"`
}

func (s *Server) claudeAuthStatus() claudeAuthStatusResponse {
	st := s.claudeAuth.monitor.State()
	resp := claudeAuthStatusResponse{
		LoginRequired:   st.LoginRequired,
		Reason:          st.Reason,
		StoppedSessions: []claudeAuthSession{},
		Login:           s.claudeAuth.flow.Status(),
		Token:           s.claudeTokenInfo(),
	}
	if !st.Since.IsZero() {
		since := st.Since
		resp.Since = &since
	}
	for _, id := range st.StoppedSessions {
		cs := claudeAuthSession{ID: id, Name: id}
		if sess, err := s.sessions.Get(id); err == nil && sess != nil {
			cs.Name, cs.Status = sess.Name, string(sess.Status)
		} else {
			// Deleted meanwhile: nothing left to resume.
			continue
		}
		resp.StoppedSessions = append(resp.StoppedSessions, cs)
	}
	return resp
}

// claudeAuthReady writes 404 unless the feature is on and initialized.
func (s *Server) claudeAuthReady(w http.ResponseWriter) bool {
	if s.claudeAuth == nil || !experimental.Enabled(claudeauth.FeatureName) {
		writeError(w, http.StatusNotFound, "claude_relogin is disabled")
		return false
	}
	return true
}

func (s *Server) getClaudeAuthStatus(w http.ResponseWriter, r *http.Request) {
	if !s.claudeAuthReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.claudeAuthStatus())
}

func (s *Server) startClaudeLogin(w http.ResponseWriter, r *http.Request) {
	if !s.claudeAuthReady(w) {
		return
	}
	s.startFlow(w, r, s.claudeAuth.flow)
}

// startFlow starts flow (login or setup-token) and returns its status.
func (s *Server) startFlow(w http.ResponseWriter, r *http.Request, flow *claudeauth.LoginFlow) {
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	st, err := flow.Start(ctx)
	if errors.Is(err, claudeauth.ErrBusy) {
		writeJSON(w, http.StatusConflict, st)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) submitClaudeLoginCode(w http.ResponseWriter, r *http.Request) {
	if !s.claudeAuthReady(w) {
		return
	}
	s.submitFlowCode(w, r, s.claudeAuth.flow)
}

// submitFlowCode types the code from the body into flow's CLI.
func (s *Server) submitFlowCode(w http.ResponseWriter, r *http.Request, flow *claudeauth.LoginFlow) {
	var req struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	st, err := flow.SubmitCode(req.Code)
	req.Code = ""
	switch {
	case errors.Is(err, claudeauth.ErrBadCode):
		writeError(w, http.StatusBadRequest, "コードが空か、使えない文字を含んでいます")
	case errors.Is(err, claudeauth.ErrNotWaiting):
		writeJSON(w, http.StatusConflict, st)
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeJSON(w, http.StatusOK, st)
	}
}

func (s *Server) cancelClaudeLogin(w http.ResponseWriter, r *http.Request) {
	if !s.claudeAuthReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.claudeAuth.flow.Cancel())
}

type claudeAuthSessionRequest struct {
	SessionID string `json:"sessionId"`
}

// resumeClaudeSession asks a session that stopped on the lost login to go on.
func (s *Server) resumeClaudeSession(w http.ResponseWriter, r *http.Request) {
	if !s.claudeAuthReady(w) {
		return
	}
	var req claudeAuthSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	if s.claudeAuth.monitor.Required() {
		writeError(w, http.StatusConflict, "Claude にまだログインしていません")
		return
	}
	if !s.claudeAuth.monitor.IsStopped(req.SessionID) {
		writeError(w, http.StatusNotFound, "session is not waiting for a resume")
		return
	}
	if err := s.sessions.SendKeys(req.SessionID, claudeResumeMessage); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.sessions.UpdateStatus(req.SessionID, session.StatusWorking)
	s.claudeAuth.monitor.RemoveStopped(req.SessionID)
	writeJSON(w, http.StatusOK, s.claudeAuthStatus())
}

// dismissClaudeSession drops a session (or, with an empty id, all of them)
// from the resume list without sending anything.
func (s *Server) dismissClaudeSession(w http.ResponseWriter, r *http.Request) {
	if !s.claudeAuthReady(w) {
		return
	}
	var req claudeAuthSessionRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	s.claudeAuth.monitor.RemoveStopped(req.SessionID)
	writeJSON(w, http.StatusOK, s.claudeAuthStatus())
}
