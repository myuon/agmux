package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/myuon/agmux/internal/automation"
)

// automationRequest is the request body for creating / updating an automation.
type automationRequest struct {
	Name         string `json:"name"`
	Prompt       string `json:"prompt"`
	TriggerType  string `json:"triggerType"`
	TriggerValue string `json:"triggerValue"`
	ProjectPath  string `json:"projectPath,omitempty"`
	Enabled      bool   `json:"enabled"`
}

func (s *Server) listAutomations(w http.ResponseWriter, r *http.Request) {
	automations, err := s.automations.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if automations == nil {
		automations = []automation.Automation{}
	}
	writeJSON(w, http.StatusOK, automations)
}

func (s *Server) createAutomation(w http.ResponseWriter, r *http.Request) {
	var req automationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	a, err := s.automations.Create(automation.CreateParams{
		Name:         req.Name,
		Prompt:       req.Prompt,
		TriggerType:  automation.TriggerType(req.TriggerType),
		TriggerValue: req.TriggerValue,
		ProjectPath:  req.ProjectPath,
		Enabled:      req.Enabled,
	})
	if err != nil {
		// Store.Create only fails on validation errors or DB errors; treat
		// validation as the common case.
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) getAutomation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	a, err := s.automations.Get(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) updateAutomation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.automations.Get(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req automationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	a, err := s.automations.Update(id, automation.UpdateParams{
		Name:         req.Name,
		Prompt:       req.Prompt,
		TriggerType:  automation.TriggerType(req.TriggerType),
		TriggerValue: req.TriggerValue,
		ProjectPath:  req.ProjectPath,
		Enabled:      req.Enabled,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.automations.Get(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err := s.automations.Delete(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

type setAutomationEnabledRequest struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) setAutomationEnabled(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req setAutomationEnabledRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := s.automations.SetEnabled(id, req.Enabled); err != nil {
		if strings.Contains(err.Error(), "not found") {
			writeError(w, http.StatusNotFound, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	a, err := s.automations.Get(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) listAutomationRuns(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := s.automations.Get(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	runs, err := s.automations.ListRuns(id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []automation.Run{}
	}
	writeJSON(w, http.StatusOK, runs)
}
