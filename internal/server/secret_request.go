package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Responses delivered to a pending request_secret escalation. They are
// constants so the agent never receives anything the user typed.
const (
	SecretResponseSaved     = "secret_saved"
	SecretResponseCancelled = "secret_cancelled"
)

// secretRequestResolveBody is the body of POST
// /api/sessions/{id}/escalate/secret. It is secretRequest plus the escalation
// ID. Do not log it.
type secretRequestResolveBody struct {
	secretRequest
	ID string `json:"id"`
}

// resolveSecretRequest stores the secret the agent asked for (request_secret)
// and then resolves the pending escalation with SecretResponseSaved. The name
// is always the one the agent requested; the body's name is ignored.
func (s *Server) resolveSecretRequest(w http.ResponseWriter, r *http.Request) {
	if !s.secretsReady(w) {
		return
	}
	sessionID := chi.URLParam(r, "id")
	var body secretRequestResolveBody
	if !decodeSecretBody(w, r, &body) {
		return
	}
	req := body.secretRequest
	body.Value = ""
	info := s.escalations.PendingSecretRequest(body.ID, sessionID)
	if info == nil {
		writeError(w, http.StatusNotFound, "secret request not found or already resolved")
		return
	}
	req.Name = info.Name
	sec, _ := s.storeSecretValue(w, r, &req)
	if sec == nil {
		return
	}
	if !s.escalations.Respond(body.ID, SecretResponseSaved) {
		// Timed out or cancelled while storing. The secret is stored anyway.
		s.logger.Warn("secret stored but request was no longer pending", "name", sec.Name, "session", sessionID)
	}
	writeJSON(w, http.StatusOK, sec)
}
