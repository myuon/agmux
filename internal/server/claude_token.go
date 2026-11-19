package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/myuon/agmux/internal/claudeauth"
	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/secret"
)

// Long-lived Claude token (`claude setup-token`, #833; part of the
// experimental "claude_relogin"). The token is kept as the secret
// CLAUDE_CODE_OAUTH_TOKEN and handed to claude sessions as that env var. It
// is never logged, returned by the API, or shown to the user.
//
// The token's creation time is the secret's CreatedAt: agmux re-creates the
// metadata row every time it stores a new token.

const claudeTokenDescription = "Claude の長期トークン（agmux が claude setup-token で作成。期限は作成から1年）"

// claudeTokenNoticePrefix starts every expiry notification; it is also how
// the "once a day" limit finds the previous one.
const claudeTokenNoticePrefix = "Claude の長期トークンの期限が"

type claudeTokenInfo struct {
	Present      bool                   `json:"present"`
	CreatedAt    *time.Time             `json:"createdAt,omitempty"`
	ExpiresAt    *time.Time             `json:"expiresAt,omitempty"`
	ExpiringSoon bool                   `json:"expiringSoon"`
	Expired      bool                   `json:"expired"`
	Flow         claudeauth.LoginStatus `json:"flow"`
}

func (s *Server) claudeNow() time.Time {
	if s.claudeAuth != nil && s.claudeAuth.now != nil {
		return s.claudeAuth.now()
	}
	return time.Now()
}

// claudeTokenMeta returns the token secret's metadata, or nil.
func (s *Server) claudeTokenMeta() *secret.Secret {
	if s.secrets == nil {
		return nil
	}
	sec, err := s.secrets.Get(claudeauth.TokenSecretName)
	if err != nil {
		return nil
	}
	return sec
}

func (s *Server) claudeTokenInfo() claudeTokenInfo {
	info := claudeTokenInfo{Flow: claudeauth.LoginStatus{Phase: claudeauth.PhaseIdle}}
	if s.claudeAuth != nil && s.claudeAuth.token != nil {
		info.Flow = s.claudeAuth.token.Status()
	}
	sec := s.claudeTokenMeta()
	if sec == nil {
		return info
	}
	created := sec.CreatedAt
	expires := claudeauth.TokenExpiry(created)
	now := s.claudeNow()
	info.Present = true
	info.CreatedAt, info.ExpiresAt = &created, &expires
	info.ExpiringSoon = claudeauth.TokenExpiringSoon(created, now)
	info.Expired = !now.Before(expires)
	return info
}

// claudeTokenBackend picks the backend for the token: the configured default,
// except 1Password, which only stores references.
func (s *Server) claudeTokenBackend() (secret.Backend, error) {
	if s.secretBackends == nil || s.secrets == nil {
		return nil, errors.New("secret store is not available")
	}
	kind := defaultSecretBackend()
	if kind == secret.Backend1Password {
		kind = secret.BackendKeychain
	}
	return s.secretBackends.For(kind)
}

// saveClaudeToken stores token as the CLAUDE_CODE_OAUTH_TOKEN secret and
// resets its creation time. Never log token.
func (s *Server) saveClaudeToken(token string) error {
	backend, err := s.claudeTokenBackend()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	name := claudeauth.TokenSecretName
	if err := backend.Set(ctx, name, token); err != nil {
		return fmt.Errorf("store value: %w", err)
	}
	if prev := s.claudeTokenMeta(); prev != nil {
		if prev.Backend != backend.Kind() {
			if old, err := s.secretBackends.For(prev.Backend); err == nil {
				if err := old.Delete(ctx, name); err != nil && !errors.Is(err, secret.ErrNotFound) {
					s.logger.Warn("delete previous claude token value failed", "backend", prev.Backend, "error", err)
				}
			}
		}
		// A new row, so CreatedAt is the new token's creation time.
		if err := s.secrets.Delete(name); err != nil && !errors.Is(err, secret.ErrNotFound) {
			return err
		}
	}
	if _, _, err := s.secrets.Upsert(secret.Secret{
		Name:        name,
		Description: claudeTokenDescription,
		Backend:     backend.Kind(),
	}); err != nil {
		return err
	}
	s.logger.Info("claude long-lived token saved", "secret", name, "backend", backend.Kind())
	return nil
}

// deleteClaudeTokenSecret removes the token (value and metadata).
func (s *Server) deleteClaudeTokenSecret(ctx context.Context) error {
	sec := s.claudeTokenMeta()
	if sec == nil {
		return secret.ErrNotFound
	}
	if backend, err := s.secretBackends.For(sec.Backend); err == nil {
		if err := backend.Delete(ctx, sec.Name); err != nil && !errors.Is(err, secret.ErrNotFound) {
			return err
		}
	}
	if err := s.secrets.Delete(sec.Name); err != nil && !errors.Is(err, secret.ErrNotFound) {
		return err
	}
	s.logger.Info("claude long-lived token deleted", "secret", sec.Name)
	return nil
}

// ClaudeSessionEnv is the session.ClaudeEnvSource: CLAUDE_CODE_OAUTH_TOKEN
// when claude_relogin is on and a token is stored, otherwise nil.
func (s *Server) ClaudeSessionEnv() map[string]string {
	if !experimental.Enabled(claudeauth.FeatureName) || s.secretBackends == nil {
		return nil
	}
	sec := s.claudeTokenMeta()
	if sec == nil {
		return nil
	}
	backend, err := s.secretBackends.For(sec.Backend)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v, err := backend.Get(ctx, sec)
	if err != nil || v == "" {
		s.logger.Warn("claude long-lived token is registered but could not be read", "backend", sec.Backend, "error", err)
		return nil
	}
	_ = s.secrets.MarkUsed(sec.Name)
	return map[string]string{claudeauth.TokenSecretName: v}
}

// checkClaudeTokenExpiry notifies (at most once a day) when the token is
// within TokenWarnBefore of its expiry.
func (s *Server) checkClaudeTokenExpiry() {
	if s.claudeAuth == nil || !experimental.Enabled(claudeauth.FeatureName) || s.sqlDB == nil {
		return
	}
	info := s.claudeTokenInfo()
	if !info.Present || !info.ExpiringSoon {
		return
	}
	since := time.Now().UTC().Add(-24 * time.Hour).Format("2006-01-02 15:04:05")
	var n int
	if err := s.sqlDB.QueryRow(
		`SELECT COUNT(*) FROM notifications WHERE message LIKE ? AND created_at > ?`,
		claudeTokenNoticePrefix+"%", since,
	).Scan(&n); err != nil {
		s.logger.Warn("check claude token notifications failed", "error", err)
		return
	}
	if n > 0 {
		return
	}
	day := info.ExpiresAt.Local().Format("2006-01-02")
	msg := claudeTokenNoticePrefix + "近づいています（期限 " + day + "）。設定画面の Experimental から作り直してください。"
	if info.Expired {
		msg = claudeTokenNoticePrefix + "切れています（期限 " + day + "）。設定画面の Experimental から作り直してください。"
	}
	if err := s.saveNotification("", "notification", msg); err != nil {
		s.logger.Warn("failed to save claude token notification", "error", err)
		return
	}
	if s.hub != nil {
		s.hub.Broadcast(Message{Type: "agent_notification", Data: map[string]interface{}{
			"sessionId":   "",
			"sessionName": "Claude",
			"message":     msg,
		}})
	}
	s.broadcastClaudeAuth()
}

// StartClaudeTokenWatcher checks the token's expiry now and then hourly.
func (s *Server) StartClaudeTokenWatcher(ctx context.Context) {
	go func() {
		s.checkClaudeTokenExpiry()
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.checkClaudeTokenExpiry()
			}
		}
	}()
}

func (s *Server) broadcastClaudeAuth() {
	if s.hub != nil && s.claudeAuth != nil {
		s.hub.Broadcast(Message{Type: "claude_auth", Data: s.claudeAuthStatus()})
	}
}

func (s *Server) startClaudeToken(w http.ResponseWriter, r *http.Request) {
	if !s.claudeAuthReady(w) {
		return
	}
	if _, err := s.claudeTokenBackend(); err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	s.startFlow(w, r, s.claudeAuth.token)
}

func (s *Server) submitClaudeTokenCode(w http.ResponseWriter, r *http.Request) {
	if !s.claudeAuthReady(w) {
		return
	}
	s.submitFlowCode(w, r, s.claudeAuth.token)
}

func (s *Server) cancelClaudeToken(w http.ResponseWriter, r *http.Request) {
	if !s.claudeAuthReady(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.claudeAuth.token.Cancel())
}

func (s *Server) deleteClaudeToken(w http.ResponseWriter, r *http.Request) {
	if !s.claudeAuthReady(w) {
		return
	}
	if s.secrets == nil || s.secretBackends == nil {
		writeError(w, http.StatusServiceUnavailable, "secret store is not available")
		return
	}
	if err := s.deleteClaudeTokenSecret(r.Context()); errors.Is(err, secret.ErrNotFound) {
		writeError(w, http.StatusNotFound, "長期トークンはありません")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.broadcastClaudeAuth()
	writeJSON(w, http.StatusOK, s.claudeAuthStatus())
}
