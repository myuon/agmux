package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"

	"github.com/myuon/agmux/internal/artifact"
)

// artifactCSP is sent with every raw artifact response. The sandbox directive
// gives the document an opaque origin even when the URL is opened directly in
// a tab, so it can never read agmux's origin (API, WebSocket, storage).
// allow-same-origin / top-navigation / popups / forms / modals are deliberately
// absent.
const artifactCSP = "sandbox allow-scripts; default-src 'none'; " +
	"script-src 'unsafe-inline' 'unsafe-eval' https:; " +
	"style-src 'unsafe-inline' https:; " +
	"img-src data: blob: https:; " +
	"font-src data: https:; " +
	"media-src data: blob: https:; " +
	"connect-src 'none'; form-action 'none'; frame-src 'none'; base-uri 'none'"

func (s *Server) mountArtifacts(r chi.Router) {
	r.Get("/sessions/{id}/artifacts", s.listArtifacts)
	r.Post("/sessions/{id}/artifacts", s.createArtifact)
	r.Get("/artifacts/{id}", s.getArtifact)
	r.Put("/artifacts/{id}", s.updateArtifact)
	r.Delete("/artifacts/{id}", s.deleteArtifact)
}

// mountArtifactRaw registers the raw content route (outside /api).
func (s *Server) mountArtifactRaw(r chi.Router) {
	r.Get("/artifacts/{id}/raw", s.rawArtifact)
}

func (s *Server) artifactStore(w http.ResponseWriter) *artifact.Store {
	if s.artifacts == nil {
		writeError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return nil
	}
	return s.artifacts
}

func (s *Server) listArtifacts(w http.ResponseWriter, r *http.Request) {
	store := s.artifactStore(w)
	if store == nil {
		return
	}
	list, err := store.List(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

type createArtifactRequest struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// createArtifact registers a file the MCP tool already copied into the
// artifacts dir. It never reads arbitrary paths: only an existing
// artifacts/<id>/index.html can be registered.
func (s *Server) createArtifact(w http.ResponseWriter, r *http.Request) {
	store := s.artifactStore(w)
	if store == nil {
		return
	}
	sessionID := chi.URLParam(r, "id")
	var req createArtifactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sess, err := s.sessions.Get(sessionID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	a, err := store.Register(sessionID, req.ID, req.Title)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, artifact.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}

	url := "/artifacts/" + a.ID
	message := "HTML を公開しました: " + a.Title
	s.recordSessionAction(sessionID, "agent_notification", message)
	if err := s.saveNotification(sessionID, "notification", message); err != nil {
		slog.Error("failed to save artifact notification", "error", err)
	}
	s.hub.Broadcast(Message{Type: "artifact_created", Data: a})
	s.hub.Broadcast(Message{
		Type: "agent_notification",
		Data: map[string]interface{}{
			"sessionId":   sessionID,
			"sessionName": sess.Name,
			"message":     message,
			"url":         url,
		},
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"artifact": a, "url": url})
}

type updateArtifactRequest struct {
	// Title replaces the title when non-empty.
	Title string `json:"title"`
	// SessionID is the session that performed the update (used only for the
	// notification). It defaults to the artifact's own session. Ownership of
	// the artifact never changes.
	SessionID string `json:"sessionId"`
}

// updateArtifact records that the content of an existing artifact was
// replaced (the MCP tool swaps the file itself, in-process). Like creation it
// takes no path: the server only bumps updated_at / title, notifies and
// broadcasts.
func (s *Server) updateArtifact(w http.ResponseWriter, r *http.Request) {
	store := s.artifactStore(w)
	if store == nil {
		return
	}
	var req updateArtifactRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	old, err := store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if p, err := store.FilePath(old.ID); err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	} else if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	notifySession := old.SessionID
	if req.SessionID != "" {
		notifySession = req.SessionID
	}
	sess, err := s.sessions.Get(notifySession)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	a, err := store.Touch(old.ID, req.Title)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, artifact.ErrNotFound) {
			status = http.StatusNotFound
		}
		writeError(w, status, err.Error())
		return
	}

	url := "/artifacts/" + a.ID
	message := "HTML を更新しました: " + a.Title
	s.recordSessionAction(notifySession, "agent_notification", message)
	if err := s.saveNotification(notifySession, "notification", message); err != nil {
		slog.Error("failed to save artifact notification", "error", err)
	}
	s.hub.Broadcast(Message{Type: "artifact_updated", Data: a})
	s.hub.Broadcast(Message{
		Type: "agent_notification",
		Data: map[string]interface{}{
			"sessionId":   notifySession,
			"sessionName": sess.Name,
			"message":     message,
			"url":         url,
		},
	})
	writeJSON(w, http.StatusOK, map[string]interface{}{"artifact": a, "url": url})
}

func (s *Server) getArtifact(w http.ResponseWriter, r *http.Request) {
	store := s.artifactStore(w)
	if store == nil {
		return
	}
	a, err := store.Get(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) deleteArtifact(w http.ResponseWriter, r *http.Request) {
	store := s.artifactStore(w)
	if store == nil {
		return
	}
	id := chi.URLParam(r, "id")
	a, err := store.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if err := store.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.hub.Broadcast(Message{Type: "artifact_deleted", Data: a})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// rawArtifact serves the stored HTML with isolation headers. It serves only
// the copied file of a registered artifact, never anything else.
func (s *Server) rawArtifact(w http.ResponseWriter, r *http.Request) {
	if s.artifacts == nil {
		http.NotFound(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := s.artifacts.Get(id); err != nil {
		http.NotFound(w, r)
		return
	}
	p, err := s.artifacts.FilePath(id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Content-Security-Policy", artifactCSP)
	h.Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}
