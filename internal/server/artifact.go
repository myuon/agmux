package server

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

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
	r.Get("/artifacts", s.listAllArtifacts)
	r.Get("/sessions/{id}/artifacts", s.listArtifacts)
	r.Post("/sessions/{id}/artifacts", s.createArtifact)
	r.Get("/artifacts/{id}", s.getArtifact)
	r.Put("/artifacts/{id}", s.updateArtifact)
	r.Delete("/artifacts/{id}", s.deleteArtifact)
}

// mountArtifactRaw registers the raw content and data file routes (outside
// /api).
func (s *Server) mountArtifactRaw(r chi.Router) {
	r.Get("/artifacts/{id}/raw", s.rawArtifact)
	// HandleFunc (all methods) so non-GET/HEAD get 405 here instead of
	// falling through to the SPA catch-all.
	r.HandleFunc("/artifacts/{id}/data/{name}", s.artifactData)
}

// artifactCSPFor returns the CSP for a raw artifact. Artifacts with data files
// may connect only to their own data path. The sandboxed document has an
// opaque origin, so 'self' can't be used: the absolute origin the browser
// used (request Host) is spelled out, for both http and https because a
// TLS-terminating proxy (e.g. tailscale serve) may sit in front. Artifacts
// without data files keep connect-src 'none'.
func artifactCSPFor(a *artifact.Artifact, host string) string {
	if a == nil || len(a.DataFiles) == 0 {
		return artifactCSP
	}
	host = strings.ToLower(host)
	if !cspHostPattern.MatchString(host) {
		return artifactCSP
	}
	p := "//" + host + "/artifacts/" + a.ID + "/data/"
	return strings.Replace(artifactCSP, "connect-src 'none'", "connect-src http:"+p+" https:"+p, 1)
}

// cspHostPattern accepts a DNS name or IPv4 address with an optional port —
// nothing that could break out of the CSP header value.
var cspHostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)

func (s *Server) artifactStore(w http.ResponseWriter) *artifact.Store {
	if s.artifacts == nil {
		writeError(w, http.StatusServiceUnavailable, "artifacts are unavailable")
		return nil
	}
	return s.artifacts
}

func (s *Server) listAllArtifacts(w http.ResponseWriter, r *http.Request) {
	store := s.artifactStore(w)
	if store == nil {
		return
	}
	list, err := store.ListAll()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
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
	// DataFiles maps name → absolute path of files the artifact may fetch
	// (see artifact.ResolveDataFiles for the restrictions).
	DataFiles map[string]string `json:"dataFiles"`
}

// resolveArtifactData validates data files against the session's project dir.
// The artifacts dir is always denied in addition to the agmux data dir.
func (s *Server) resolveArtifactData(files map[string]string, projectPath string) (*artifact.DataFiles, error) {
	return artifact.ResolveDataFiles(files, projectPath, s.artifacts.Dir())
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
	var data *artifact.DataFiles
	if len(req.DataFiles) > 0 {
		if data, err = s.resolveArtifactData(req.DataFiles, sess.ProjectPath); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	a, err := store.Register(sessionID, req.ID, req.Title, data)
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
	// DataFiles replaces the data file mapping when present (an empty object
	// clears it); omitted or null keeps the current one. Paths are validated
	// against the project dir of the session performing the update.
	DataFiles *map[string]string `json:"dataFiles"`
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
	var data *artifact.DataFiles
	if req.DataFiles != nil {
		if data, err = s.resolveArtifactData(*req.DataFiles, sess.ProjectPath); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	a, err := store.Touch(old.ID, req.Title, data)
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
	a, err := s.artifacts.Get(id)
	if err != nil {
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
	h.Set("Content-Security-Policy", artifactCSPFor(a, r.Host))
	h.Set("Cache-Control", "private, no-store")
	if len(a.DataFiles) == 0 {
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
		return
	}
	// Artifacts with data files get the agmuxData helper (see
	// artifact_helper.go). No ETag/Content-Length is sent for raw artifacts,
	// so nothing else has to reflect the modified body.
	body, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "failed to read artifact", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(injectArtifactDataHelper(body, a.ID))
}

// artifactDataContentType picks the Content-Type from the registered name.
func artifactDataContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".json":
		return "application/json; charset=utf-8"
	case ".md", ".markdown", ".txt":
		return "text/plain; charset=utf-8"
	case ".csv":
		return "text/csv; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// artifactData serves a whitelisted data file of an artifact, read fresh from
// disk each time. Only names registered for that artifact are served and the
// stored path is re-validated on every request (still a regular file inside
// the registered project dir, not under the agmux data dir), so a later
// symlink swap can't escape. Anything else is a 404; files over
// artifact.MaxDataSize get 413.
//
// The artifact document is sandboxed without allow-same-origin, so its fetch
// arrives with Origin "null": that origin is allowed via CORS (no
// credentials). Same-origin requests need no CORS headers.
func (s *Server) artifactData(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Vary", "Origin")
	if r.Header.Get("Origin") == "null" {
		h.Set("Access-Control-Allow-Origin", "null")
	}
	if s.artifacts == nil {
		http.NotFound(w, r)
		return
	}
	name := chi.URLParam(r, "name")
	if !artifact.ValidDataName(name) {
		http.NotFound(w, r)
		return
	}
	a, err := s.artifacts.Get(chi.URLParam(r, "id"))
	if err != nil || a.Data() == nil {
		http.NotFound(w, r)
		return
	}
	path, ok := a.Data().Paths[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	body, err := artifact.ReadDataFile(a.Data().Root, path, s.artifacts.Dir())
	if errors.Is(err, artifact.ErrDataTooLarge) {
		http.Error(w, "data file is larger than 5 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.Set("Content-Type", artifactDataContentType(name))
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}
