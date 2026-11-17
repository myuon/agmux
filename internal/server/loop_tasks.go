package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/experimental"
)

func (s *Server) requireLoops(w http.ResponseWriter) bool {
	if !experimental.Enabled(automation.FeatureLoops) {
		writeError(w, http.StatusNotFound, "loops is disabled")
		return false
	}
	return true
}

// loopTaskTarget parses the {id} / {tid} URL params and checks that the
// automation exists. tid is only parsed when withTask is true.
func (s *Server) loopTaskTarget(w http.ResponseWriter, r *http.Request, withTask bool) (string, int64, bool) {
	if !s.requireLoops(w) {
		return "", 0, false
	}
	id := chi.URLParam(r, "id")
	if _, err := s.automations.Get(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return "", 0, false
	}
	var tid int64
	if withTask {
		var err error
		tid, err = strconv.ParseInt(chi.URLParam(r, "tid"), 10, 64)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid task id")
			return "", 0, false
		}
	}
	return id, tid, true
}

func (s *Server) listAutomationTasks(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.loopTaskTarget(w, r, false)
	if !ok {
		return
	}
	tasks, err := s.automations.ListTasks(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) addAutomationTask(w http.ResponseWriter, r *http.Request) {
	id, _, ok := s.loopTaskTarget(w, r, false)
	if !ok {
		return
	}
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	t, err := s.automations.AddTask(id, req.Title, req.Body, automation.CreatedByUser)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) updateAutomationTask(w http.ResponseWriter, r *http.Request) {
	id, tid, ok := s.loopTaskTarget(w, r, true)
	if !ok {
		return
	}
	var req struct {
		Title  *string                `json:"title"`
		Body   *string                `json:"body"`
		Status *automation.TaskStatus `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, err := s.automations.GetTask(id, tid); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	t, err := s.automations.UpdateTask(id, tid, automation.UpdateTaskParams{Title: req.Title, Body: req.Body, Status: req.Status})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) deleteAutomationTask(w http.ResponseWriter, r *http.Request) {
	id, tid, ok := s.loopTaskTarget(w, r, true)
	if !ok {
		return
	}
	if err := s.automations.DeleteTask(id, tid); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// loopSessionAutomation resolves the automation of the calling session (used
// by the MCP loop_task_* tools).
func (s *Server) loopSessionAutomation(w http.ResponseWriter, r *http.Request) (string, bool) {
	if !s.requireLoops(w) {
		return "", false
	}
	autoID, err := s.automations.AutomationIDForSession(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return "", false
	}
	if autoID == "" {
		writeError(w, http.StatusBadRequest, "このセッションは Automation から起動されたものではないため、ループのタスクは使えません")
		return "", false
	}
	return autoID, true
}

func (s *Server) loopTaskAdd(w http.ResponseWriter, r *http.Request) {
	autoID, ok := s.loopSessionAutomation(w, r)
	if !ok {
		return
	}
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	t, err := s.automations.AddTask(autoID, req.Title, req.Body, automation.CreatedByAgent)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// loopTaskNext returns {"task": null} when nothing is pending.
func (s *Server) loopTaskNext(w http.ResponseWriter, r *http.Request) {
	autoID, ok := s.loopSessionAutomation(w, r)
	if !ok {
		return
	}
	var runID *int64
	run, err := s.automations.RunForSession(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if run != nil {
		runID = &run.ID
	}
	t, err := s.automations.NextTask(autoID, runID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"task": t})
}

func (s *Server) loopTaskDone(w http.ResponseWriter, r *http.Request) {
	autoID, ok := s.loopSessionAutomation(w, r)
	if !ok {
		return
	}
	var req struct {
		TaskID int64  `json:"taskId"`
		Status string `json:"status"`
		Result string `json:"result"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	t, err := s.automations.FinishTask(autoID, req.TaskID, automation.TaskStatus(req.Status), req.Result)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, t)
}
