package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/myuon/agmux/internal/config"
	"github.com/myuon/agmux/internal/secret"
)

// Secret API. Values are accepted on registration only and are written
// straight to the value backend; they are never stored in the DB, logged, or
// returned. There is intentionally NO endpoint that returns a secret value:
// `agmux secret exec` reads values directly from the backend in the CLI
// process.

// maxSecretRequestBytes bounds the registration body.
const maxSecretRequestBytes = 2 << 20

// secretRequest is the body of POST /api/secrets. Do not log it.
type secretRequest struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Backend         string   `json:"backend,omitempty"`
	Value           string   `json:"value,omitempty"`
	Ref             string   `json:"ref,omitempty"`
	AllowedProjects []string `json:"allowedProjects,omitempty"`
}

// secretAuthorizeRequest is the body of POST /api/secrets/authorize.
type secretAuthorizeRequest struct {
	Names     []string `json:"names"`
	SessionID string   `json:"sessionId,omitempty"`
	// Cwd is used as the project path when SessionID is empty or unknown.
	Cwd string `json:"cwd,omitempty"`
	// Command / ArgCount describe the command for the audit log of denied
	// attempts: argv[0] (reduced to its basename server-side) and the number
	// of remaining args. Full arguments are never sent.
	Command  string `json:"command,omitempty"`
	ArgCount int    `json:"argCount,omitempty"`
}

type secretAuthorizeResponse struct {
	// Project is the project path the scope check was performed against.
	Project string          `json:"project"`
	Secrets []secret.Secret `json:"secrets"`
}

// secretUsageRequest is the body of POST /api/secrets/{name}/used. Every
// field is optional (older CLIs only send sessionId).
type secretUsageRequest struct {
	SessionID string `json:"sessionId,omitempty"`
	Cwd       string `json:"cwd,omitempty"`
	// Command is argv[0]; only its basename is stored. Full arguments are
	// never sent or stored, only their count.
	Command  string `json:"command,omitempty"`
	ArgCount int    `json:"argCount,omitempty"`
	ExitCode *int   `json:"exitCode,omitempty"`
}

func (s *Server) mountSecrets(r chi.Router) {
	r.Get("/secrets", s.listSecrets)
	r.Post("/secrets", s.registerSecret)
	r.Post("/secrets/authorize", s.authorizeSecrets)
	r.Get("/secrets/usages", s.listSecretUsages)
	r.Get("/secrets/{name}", s.getSecret)
	r.Delete("/secrets/{name}", s.deleteSecret)
	r.Post("/secrets/{name}/used", s.secretUsed)
}

func (s *Server) secretsReady(w http.ResponseWriter) bool {
	if s.secrets == nil || s.secretBackends == nil {
		writeError(w, http.StatusServiceUnavailable, "secret store is not available")
		return false
	}
	return true
}

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.secretsReady(w) {
		return
	}
	list, err := s.secrets.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) getSecret(w http.ResponseWriter, r *http.Request) {
	if !s.secretsReady(w) {
		return
	}
	sec, err := s.secrets.Get(chi.URLParam(r, "name"))
	if errors.Is(err, secret.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sec)
}

// defaultSecretBackend returns the configured default backend kind.
func defaultSecretBackend() secret.BackendKind {
	cfg, err := config.Load()
	if err != nil {
		return secret.DefaultBackend
	}
	return secret.BackendKind(cfg.Secret.EffectiveDefaultBackend())
}

// decodeSecretBody reads a body that may carry a secret value into v.
// Errors never quote the body.
func decodeSecretBody(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSecretRequestBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "request body too large or unreadable")
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		// Do not include the decoder error: it may quote parts of the body.
		writeError(w, http.StatusBadRequest, "invalid request body")
		return false
	}
	return true
}

func (s *Server) registerSecret(w http.ResponseWriter, r *http.Request) {
	if !s.secretsReady(w) {
		return
	}
	var req secretRequest
	if !decodeSecretBody(w, r, &req) {
		return
	}
	s.storeSecret(w, r, &req)
}

// storeSecretValue writes the value to the backend and upserts the metadata.
// On failure it writes an error response and returns nil; on success it
// returns the stored metadata and whether it was newly created without
// writing a response. req.Value is cleared.
func (s *Server) storeSecretValue(w http.ResponseWriter, r *http.Request, req *secretRequest) (*secret.Secret, bool) {
	value := req.Value
	req.Value = ""

	if err := secret.ValidateName(req.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	kind := secret.BackendKind(req.Backend)
	if kind == "" {
		kind = defaultSecretBackend()
	}
	backend, err := s.secretBackends.For(kind)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	if kind == secret.Backend1Password {
		// Only a reference is stored; a value is never accepted.
		if value != "" {
			writeError(w, http.StatusBadRequest, "the 1password backend does not accept a value; pass an op:// ref")
			return nil, false
		}
		if err := secret.ValidateOPRef(req.Ref); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return nil, false
		}
	} else if value == "" {
		writeError(w, http.StatusBadRequest, "value is required")
		return nil, false
	} else {
		req.Ref = ""
	}

	prev, prevErr := s.secrets.Get(req.Name)
	if prevErr != nil && !errors.Is(prevErr, secret.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, prevErr.Error())
		return nil, false
	}

	if kind != secret.Backend1Password {

		if err := backend.Set(r.Context(), req.Name, value); err != nil {
			s.logger.Error("store secret value failed", "name", req.Name, "backend", kind, "error", err)
			writeError(w, http.StatusInternalServerError, "store secret value: "+err.Error())
			return nil, false
		}
	}
	sec, created, err := s.secrets.Upsert(secret.Secret{
		Name:            req.Name,
		Description:     req.Description,
		Backend:         kind,
		Ref:             req.Ref,
		AllowedProjects: req.AllowedProjects,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return nil, false
	}
	// The secret moved to another backend: drop the stale copy.
	if prev != nil && prev.Backend != kind {
		if old, err := s.secretBackends.For(prev.Backend); err == nil {
			if err := old.Delete(r.Context(), req.Name); err != nil && !errors.Is(err, secret.ErrNotFound) {
				s.logger.Warn("delete previous secret value failed", "name", req.Name, "backend", prev.Backend, "error", err)
			}
		}
	}
	s.logger.Info("secret registered", "name", sec.Name, "backend", sec.Backend, "created", created)
	return sec, created
}

// storeSecret is storeSecretValue plus the success response.
func (s *Server) storeSecret(w http.ResponseWriter, r *http.Request, req *secretRequest) {
	sec, created := s.storeSecretValue(w, r, req)
	if sec == nil {
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, sec)
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request) {
	if !s.secretsReady(w) {
		return
	}
	name := chi.URLParam(r, "name")
	sec, err := s.secrets.Get(name)
	if errors.Is(err, secret.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if backend, err := s.secretBackends.For(sec.Backend); err == nil {
		if err := backend.Delete(r.Context(), name); err != nil && !errors.Is(err, secret.ErrNotFound) {
			writeError(w, http.StatusInternalServerError, "delete secret value: "+err.Error())
			return
		}
	}
	if err := s.secrets.Delete(name); err != nil && !errors.Is(err, secret.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.logger.Info("secret deleted", "name", name)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// authorizeSecrets resolves metadata for `agmux secret exec` and checks each
// secret's project scope against the caller's session project (falling back
// to cwd). It returns metadata only; the CLI reads values from the backend
// itself.
func (s *Server) authorizeSecrets(w http.ResponseWriter, r *http.Request) {
	if !s.secretsReady(w) {
		return
	}
	var req secretAuthorizeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// cwd is only trusted outside a session. A session ID that does not
	// resolve is an error rather than a silent fallback to cwd.
	project := req.Cwd
	if req.SessionID != "" {
		if s.sessions == nil {
			writeError(w, http.StatusServiceUnavailable, "session lookup is not available")
			return
		}
		sess, err := s.sessions.Get(req.SessionID)
		if err != nil {
			writeError(w, http.StatusNotFound, "session not found: "+req.SessionID)
			return
		}
		project = sess.ProjectPath
	}
	resp := secretAuthorizeResponse{Project: project, Secrets: []secret.Secret{}}
	for _, name := range req.Names {
		sec, err := s.secrets.Get(name)
		if errors.Is(err, secret.ErrNotFound) {
			writeError(w, http.StatusNotFound, "secret not found: "+name)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if !secret.ProjectAllowed(sec.AllowedProjects, project) {
			s.logger.Warn("secret use denied by project scope", "name", name, "project", project, "session", req.SessionID)
			if _, err := s.secrets.RecordUsage(secret.Usage{
				SecretName:  name,
				SessionID:   req.SessionID,
				ProjectPath: project,
				Cwd:         req.Cwd,
				Command:     req.Command,
				ArgCount:    req.ArgCount,
				Status:      secret.UsageDenied,
			}); err != nil {
				s.logger.Warn("record denied secret usage failed", "name", name, "error", err)
			}
			writeError(w, http.StatusForbidden, "secret "+name+" is not allowed in project "+project)
			return
		}
		resp.Secrets = append(resp.Secrets, *sec)
	}
	writeJSON(w, http.StatusOK, resp)
}

// secretUsed is called (best-effort) by `agmux secret exec` after it injected
// the secret into a child process.
func (s *Server) secretUsed(w http.ResponseWriter, r *http.Request) {
	if !s.secretsReady(w) {
		return
	}
	name := chi.URLParam(r, "name")
	var req secretUsageRequest
	_ = json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req)
	if err := s.recordSecretUsage(name, req); err != nil {
		if errors.Is(err, secret.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// recordSecretUsage is the single hook point for successful secret usage: it
// updates last_used_at and appends an audit log entry. The project is the
// session's project when the session is known, else cwd.
func (s *Server) recordSecretUsage(name string, req secretUsageRequest) error {
	project := req.Cwd
	if req.SessionID != "" && s.sessions != nil {
		if sess, err := s.sessions.Get(req.SessionID); err == nil {
			project = sess.ProjectPath
		}
	}
	_, err := s.secrets.RecordUsage(secret.Usage{
		SecretName:  name,
		SessionID:   req.SessionID,
		ProjectPath: project,
		Cwd:         req.Cwd,
		Command:     req.Command,
		ArgCount:    req.ArgCount,
		ExitCode:    req.ExitCode,
		Status:      secret.UsageUsed,
	})
	return err
}

// listSecretUsages serves GET /api/secrets/usages?name=&limit= (newest first).
func (s *Server) listSecretUsages(w http.ResponseWriter, r *http.Request) {
	if !s.secretsReady(w) {
		return
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = n
	}
	list, err := s.secrets.ListUsages(r.URL.Query().Get("name"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}
