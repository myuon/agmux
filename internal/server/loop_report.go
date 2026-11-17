package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/experimental"
)

// loopsEnabled reports whether the experimental `loops` feature is on.
func loopsEnabled() bool { return experimental.Enabled("loops") }

// listAutomationReports returns an automation's runs (oldest first) with the
// loop_report results. Hidden (404) while `loops` is off.
func (s *Server) listAutomationReports(w http.ResponseWriter, r *http.Request) {
	if !loopsEnabled() {
		http.NotFound(w, r)
		return
	}
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
	reports, err := s.automations.ListRunReports(id, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, reports)
}

// postLoopReport records a loop_report for the automation run that created
// the given session. Hidden (404) while `loops` is off.
func (s *Server) postLoopReport(w http.ResponseWriter, r *http.Request) {
	if !loopsEnabled() {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Metrics map[string]float64      `json:"metrics"`
		Summary string                  `json:"summary"`
		Links   []automation.ReportLink `json:"links"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	run, err := s.automations.FindRunBySession(chi.URLParam(r, "id"))
	if errors.Is(err, automation.ErrNoRunForSession) {
		writeError(w, http.StatusBadRequest, "this session was not created by an automation; loop_report is only available to automation sessions")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rep, err := s.automations.SaveReport(run.ID, req.Metrics, req.Summary, req.Links)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// getAutomationTimeline returns the loop's chronological events (newest
// first). Hidden (404) while `loops` is off.
func (s *Server) getAutomationTimeline(w http.ResponseWriter, r *http.Request) {
	if !loopsEnabled() {
		http.NotFound(w, r)
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := s.automations.Get(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var before time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid before (RFC3339 expected)")
			return
		}
		before = t
	}
	limit := 0
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	tl, err := s.automations.Timeline(id, before, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tl)
}
