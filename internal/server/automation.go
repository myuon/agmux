package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/experimental"
)

// automationRequest is the request body for creating / updating an automation.
type automationRequest struct {
	Name         string `json:"name"`
	Prompt       string `json:"prompt"`
	TriggerType  string `json:"triggerType"`
	TriggerValue string `json:"triggerValue"`
	ProjectPath  string `json:"projectPath,omitempty"`
	// Model is optional. On update, omitting it keeps the stored model and an
	// explicit "" clears it.
	Model   *string `json:"model,omitempty"`
	Enabled bool    `json:"enabled"`
	// MaxRuns / Until are loop safety valves (experimental "loops"). On
	// update, omitting keeps the stored value; maxRuns 0 / until "" clear it.
	MaxRuns *int    `json:"maxRuns,omitempty"`
	Until   *string `json:"until,omitempty"` // RFC3339
	// WatchMetric / WatchDirection select the metric watched for stagnation
	// (experimental "loops"). On update, omitting keeps the stored value and
	// "" clears it.
	WatchMetric    *string `json:"watchMetric,omitempty"`
	WatchDirection *string `json:"watchDirection,omitempty"`
	// WatchTarget is the goal of WatchMetric: a number, or null / "" to clear
	// it on update. Absent keeps the stored value.
	WatchTarget json.RawMessage `json:"watchTarget,omitempty"`
	// CreatedBySessionID records the creating session on create (#805). On
	// update, omitting it keeps the stored value and "" clears it, so the
	// automation is grouped by projectPath instead of the creator's project.
	CreatedBySessionID *string `json:"createdBySessionId,omitempty"`
}

// checkLoopFeatures rejects queue triggers and safety valves while the
// experimental "loops" feature is off. It also parses the until timestamp.
func checkLoopFeatures(req automationRequest) (until *time.Time, err error) {
	if _, _, terr := parseWatchTarget(req.WatchTarget); terr != nil {
		return nil, terr
	}
	if req.Until != nil && *req.Until != "" {
		t, perr := time.Parse(time.RFC3339, *req.Until)
		if perr != nil {
			return nil, fmt.Errorf("invalid until %q (RFC3339 expected): %w", *req.Until, perr)
		}
		until = &t
	}
	if experimental.Enabled(automation.FeatureLoops) {
		return until, nil
	}
	target, _, terr := parseWatchTarget(req.WatchTarget)
	if terr != nil {
		return nil, terr
	}
	if automation.TriggerType(req.TriggerType) == automation.TriggerQueue {
		return nil, fmt.Errorf("queue trigger requires the experimental feature %q", automation.FeatureLoops)
	}
	if (req.MaxRuns != nil && *req.MaxRuns != 0) || until != nil {
		return nil, fmt.Errorf("maxRuns / until require the experimental feature %q", automation.FeatureLoops)
	}
	if deref(req.WatchMetric) != "" || deref(req.WatchDirection) != "" || target != nil {
		return nil, fmt.Errorf("watchMetric / watchDirection / watchTarget require the experimental feature %q", automation.FeatureLoops)
	}
	return until, nil
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
	if !experimental.Enabled(automation.FeatureLoops) {
		writeJSON(w, http.StatusOK, automations)
		return
	}
	feeders, feeds, err := s.automations.Relations()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]automationWithRelations, 0, len(automations))
	for _, a := range automations {
		out = append(out, withRelations(a, feeders, feeds))
	}
	writeJSON(w, http.StatusOK, out)
}

// automationWithRelations adds the loop relations (loops feature only).
type automationWithRelations struct {
	automation.Automation
	Feeders []automation.Ref `json:"feeders"`
	Feeds   []automation.Ref `json:"feeds"`
}

func withRelations(a automation.Automation, feeders, feeds map[string][]automation.Ref) automationWithRelations {
	r := automationWithRelations{Automation: a, Feeders: feeders[a.ID], Feeds: feeds[a.ID]}
	if r.Feeders == nil {
		r.Feeders = []automation.Ref{}
	}
	if r.Feeds == nil {
		r.Feeds = []automation.Ref{}
	}
	return r
}

func (s *Server) createAutomation(w http.ResponseWriter, r *http.Request) {
	var req automationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	until, err := checkLoopFeatures(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	maxRuns := 0
	if req.MaxRuns != nil {
		maxRuns = *req.MaxRuns
	}
	target, _, _ := parseWatchTarget(req.WatchTarget)
	a, err := s.automations.Create(automation.CreateParams{
		WatchTarget:    target,
		MaxRuns:        maxRuns,
		Until:          until,
		WatchMetric:    deref(req.WatchMetric),
		WatchDirection: automation.WatchDirection(deref(req.WatchDirection)),
		Name:           req.Name,
		Prompt:         req.Prompt,
		TriggerType:    automation.TriggerType(req.TriggerType),
		TriggerValue:   req.TriggerValue,
		ProjectPath:    req.ProjectPath,
		Model:          deref(req.Model),
		Enabled:        req.Enabled,

		CreatedBySessionID: deref(req.CreatedBySessionID),
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
	if experimental.Enabled(automation.FeatureLoops) {
		feeders, feeds, err := s.automations.Relations()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, withRelations(*a, feeders, feeds))
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
	until, err := checkLoopFeatures(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	target, clearTarget, _ := parseWatchTarget(req.WatchTarget)
	a, err := s.automations.Update(id, automation.UpdateParams{
		WatchTarget:      target,
		ClearWatchTarget: clearTarget,
		MaxRuns:          req.MaxRuns,
		Until:            until,
		ClearUntil:       req.Until != nil && *req.Until == "",
		WatchMetric:      req.WatchMetric,
		WatchDirection:   watchDirPtr(req.WatchDirection),
		Name:             req.Name,
		Prompt:           req.Prompt,
		TriggerType:      automation.TriggerType(req.TriggerType),
		TriggerValue:     req.TriggerValue,
		ProjectPath:      req.ProjectPath,
		Model:            req.Model,
		Enabled:          req.Enabled,

		CreatedBySessionID: req.CreatedBySessionID,
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

// parseWatchTarget decodes the watchTarget field. set is false when the field
// was absent; clear is true for null / "". A number yields a non-nil value.
func parseWatchTarget(raw json.RawMessage) (value *float64, clear bool, err error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, false, fmt.Errorf("invalid watchTarget")
	}
	switch t := v.(type) {
	case nil:
		return nil, true, nil
	case string:
		if t == "" {
			return nil, true, nil
		}
	case float64:
		return &t, false, nil
	}
	return nil, false, fmt.Errorf("watchTarget must be a number, null or an empty string")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func watchDirPtr(p *string) *automation.WatchDirection {
	if p == nil {
		return nil
	}
	d := automation.WatchDirection(*p)
	return &d
}
