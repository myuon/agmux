package server

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/experimental"
)

// featureProjects is the experimental flag for project / coordinator grouping.
const featureProjects = "projects"

// projectsEnabled reports whether the experimental `projects` feature is on.
func projectsEnabled() bool { return experimental.Enabled(featureProjects) }

// projSession is the slice of a sessions row the project model needs.
type projSession struct {
	ID, Name, Status, Type, Path string
	AutomationID, CreatedBy      string
	Goal                         string
	ParentID                     string
	CreatedAt, UpdatedAt         time.Time
}

// eligibleCoordinator reports whether the session can be a coordinator: a
// regular (worker) session that is not an automation run and not archived.
// Controller and external sessions are excluded by type; ephemeral sessions
// are short-lived sub-sessions and are excluded too.
func (p projSession) eligibleCoordinator() bool {
	return p.AutomationID == "" && p.Type == "worker" && p.Status != "archived"
}

// projectModel is a point-in-time resolution of projects, their coordinators
// and membership. Everything is computed from a handful of queries so the
// list stays cheap with hundreds of sessions.
type projectModel struct {
	sessions    []projSession
	byID        map[string]projSession
	automations []automation.Automation
	autoByID    map[string]automation.Automation
	// coordinator maps project path -> coordinator session id ("" = none).
	coordinator map[string]string
	// coordPath maps coordinator session id -> its project path.
	coordPath map[string]string
	// stored is the projects table as read.
	stored map[string]string
}

func (s *Server) loadProjectModel() (*projectModel, error) {
	m := &projectModel{
		byID:        map[string]projSession{},
		autoByID:    map[string]automation.Automation{},
		coordinator: map[string]string{},
		coordPath:   map[string]string{},
		stored:      map[string]string{},
	}
	rows, err := s.sqlDB.Query(
		`SELECT id, name, status, type, project_path, COALESCE(automation_id, ''), created_by_session_id, COALESCE(parent_session_id, ''), COALESCE(goal, ''), created_at, updated_at
		 FROM sessions WHERE type != 'controller' ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p projSession
		if err := rows.Scan(&p.ID, &p.Name, &p.Status, &p.Type, &p.Path, &p.AutomationID, &p.CreatedBy, &p.ParentID, &p.Goal, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		m.sessions = append(m.sessions, p)
		m.byID[p.ID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if m.automations, err = s.automations.List(); err != nil {
		return nil, err
	}
	for _, a := range m.automations {
		m.autoByID[a.ID] = a
	}

	prow, err := s.sqlDB.Query(`SELECT path, coordinator_session_id FROM projects`)
	if err != nil {
		return nil, err
	}
	defer prow.Close()
	for prow.Next() {
		var path, id string
		if err := prow.Scan(&path, &id); err != nil {
			return nil, err
		}
		m.stored[path] = id
	}
	if err := prow.Err(); err != nil {
		return nil, err
	}

	m.resolveCoordinators()
	return m, nil
}

// resolveCoordinators decides each project's coordinator:
//  1. the stored one while it is still an eligible session of that path
//     (covers manual reassignment);
//  2. otherwise the oldest eligible session of the path that was not created
//     by another session (UI / API origin);
//  3. otherwise the oldest eligible session of the path that is not itself
//     attached to another project's coordinator.
//
// A stored coordinator that was archived or deleted therefore moves on to the
// next oldest regular session without any hook on archive / delete.
func (m *projectModel) resolveCoordinators() {
	assign := func(path, id string) {
		m.coordinator[path] = id
		m.coordPath[id] = path
	}
	for path, id := range m.stored {
		if p, ok := m.byID[id]; ok && p.eligibleCoordinator() && p.Path == path {
			assign(path, id)
		}
	}
	// sessions is ordered oldest first.
	for _, p := range m.sessions {
		if !p.eligibleCoordinator() || m.coordinator[p.Path] != "" {
			continue
		}
		if _, creatorExists := m.byID[p.CreatedBy]; p.CreatedBy == "" || !creatorExists {
			assign(p.Path, p.ID)
		}
	}
	for _, p := range m.sessions {
		if !p.eligibleCoordinator() || m.coordinator[p.Path] != "" {
			continue
		}
		if _, byCoord := m.coordPath[p.CreatedBy]; !byCoord {
			assign(p.Path, p.ID)
		}
	}
}

// automationPath is the project an automation belongs to: its creator's
// project when the creator is a coordinator, else its own project_path.
// "" means the controller area (no project).
func (m *projectModel) automationPath(a automation.Automation) string {
	if a.CreatedBySessionID != "" {
		if path, ok := m.coordPath[a.CreatedBySessionID]; ok {
			return path
		}
	}
	return a.ProjectPath
}

// sessionPath is the project a session belongs to ("" = none).
func (m *projectModel) sessionPath(p projSession) string {
	if path, ok := m.coordPath[p.ID]; ok {
		return path
	}
	if p.AutomationID != "" {
		if a, ok := m.autoByID[p.AutomationID]; ok {
			return m.automationPath(a)
		}
		return p.Path
	}
	if p.CreatedBy != "" {
		if path, ok := m.coordPath[p.CreatedBy]; ok {
			return path
		}
	}
	return p.Path
}

// projectData is one project's membership.
type projectData struct {
	path        string
	coordinator *projSession
	// subs are non-coordinator, non-archived sessions (including automation
	// run sessions).
	subs        []projSession
	automations []automation.Automation
}

func (m *projectModel) projects() map[string]*projectData {
	out := map[string]*projectData{}
	get := func(path string) *projectData {
		d, ok := out[path]
		if !ok {
			d = &projectData{path: path}
			out[path] = d
		}
		return d
	}
	for _, p := range m.sessions {
		if p.Status == "archived" {
			continue
		}
		path := m.sessionPath(p)
		if path == "" {
			continue
		}
		d := get(path)
		if m.coordinator[path] == p.ID {
			cp := p
			d.coordinator = &cp
		} else {
			d.subs = append(d.subs, p)
		}
	}
	for _, a := range m.automations {
		if path := m.automationPath(a); path != "" {
			d := get(path)
			d.automations = append(d.automations, a)
		}
	}
	return out
}

// persistCoordinators records resolved coordinators that differ from the
// stored ones, so an automatic move (archive / delete) sticks.
func (s *Server) persistCoordinators(m *projectModel, projects map[string]*projectData) {
	for path := range projects {
		want := m.coordinator[path]
		cur, exists := m.stored[path]
		if (exists && cur == want) || (!exists && want == "") {
			continue
		}
		s.upsertCoordinator(path, want)
	}
}

func (s *Server) upsertCoordinator(path, id string) {
	now := time.Now()
	if _, err := s.sqlDB.Exec(
		`INSERT INTO projects (path, coordinator_session_id, created_at, updated_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET coordinator_session_id = excluded.coordinator_session_id, updated_at = excluded.updated_at`,
		path, id, now, now); err != nil {
		s.logger.Warn("projects: persist coordinator failed", "path", path, "error", err)
	}
}

// --- API types ---

type projectCoordinator struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type projectAutomationCounts struct {
	Running int `json:"running"`
	Waiting int `json:"waiting"`
	Stopped int `json:"stopped"`
}

type projectSummary struct {
	Path               string                  `json:"path"`
	Name               string                  `json:"name"`
	Coordinator        *projectCoordinator     `json:"coordinator"`
	ActiveSessions     int                     `json:"activeSessions"`
	Automations        projectAutomationCounts `json:"automations"`
	PendingEscalations int                     `json:"pendingEscalations"`
	LastUpdatedAt      *time.Time              `json:"lastUpdatedAt"`
}

type projectSession struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Status             string    `json:"status"`
	Type               string    `json:"type"`
	ProjectPath        string    `json:"projectPath"`
	CreatedBySessionID string    `json:"createdBySessionId,omitempty"`
	ParentSessionID    string    `json:"parentSessionId,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// projectAutomation is an automation in a project's detail. The loop-specific
// fields are only set while the experimental "loops" feature is on.
type projectAutomation struct {
	ID                 string                 `json:"id"`
	Name               string                 `json:"name"`
	Enabled            bool                   `json:"enabled"`
	TriggerType        string                 `json:"triggerType"`
	TriggerValue       string                 `json:"triggerValue"`
	ProjectPath        string                 `json:"projectPath,omitempty"`
	CreatedBySessionID string                 `json:"createdBySessionId,omitempty"`
	State              string                 `json:"state"`
	LastRunAt          *time.Time             `json:"lastRunAt"`
	NextFireAt         *time.Time             `json:"nextFireAt"`
	CreatedAt          time.Time              `json:"createdAt"`
	RecentRuns         []automationRunSummary `json:"recentRuns"`
	// loops only
	PendingTasks  *int               `json:"pendingTasks,omitempty"`
	StoppedReason string             `json:"stoppedReason,omitempty"`
	LatestReport  *automation.Report `json:"latestReport,omitempty"`
}

type projectEscalation struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Message   string `json:"message"`
}

type projectDetail struct {
	projectSummary
	Sessions       []projectSession    `json:"sessions"`
	AutomationList []projectAutomation `json:"automationList"`
	Escalations    []projectEscalation `json:"escalations"`
}

func (s *Server) projectEscalations(m *projectModel, path string) []projectEscalation {
	out := []projectEscalation{}
	if s.escalations == nil {
		return out
	}
	for _, e := range s.escalations.Pending() {
		sess, ok := m.byID[e.SessionID]
		if !ok || m.sessionPath(sess) != path {
			continue
		}
		out = append(out, projectEscalation{ID: e.ID, SessionID: e.SessionID, Message: e.Message})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// buildProject builds the summary (and, when detail is set, the lists) of a
// project. Automation states reuse buildAutomationSummary.
func (s *Server) buildProject(m *projectModel, d *projectData, detail bool, now time.Time) (*projectDetail, error) {
	sum := projectSummary{Path: d.path, Name: filepath.Base(d.path)}
	var last time.Time
	touch := func(t time.Time) {
		if t.After(last) {
			last = t
		}
	}
	if d.coordinator != nil {
		sum.Coordinator = &projectCoordinator{ID: d.coordinator.ID, Name: d.coordinator.Name, Status: d.coordinator.Status}
		touch(d.coordinator.UpdatedAt)
	}
	pd := &projectDetail{Sessions: []projectSession{}, AutomationList: []projectAutomation{}, Escalations: s.projectEscalations(m, d.path)}
	sum.PendingEscalations = len(pd.Escalations)
	loops := loopsEnabled()

	for _, p := range d.subs {
		if isActiveSessionStatus(p.Status) {
			sum.ActiveSessions++
		}
		touch(p.UpdatedAt)
		if detail && p.AutomationID == "" {
			pd.Sessions = append(pd.Sessions, projectSession{
				ID: p.ID, Name: p.Name, Status: p.Status, Type: p.Type, ProjectPath: p.Path,
				CreatedBySessionID: p.CreatedBy, ParentSessionID: p.ParentID,
				CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
			})
		}
	}
	for _, a := range d.automations {
		as, err := s.buildAutomationSummary(a, now)
		if err != nil {
			return nil, err
		}
		switch as.State {
		case loopStateRunning:
			sum.Automations.Running++
		case loopStateWaiting:
			sum.Automations.Waiting++
		default:
			sum.Automations.Stopped++
		}
		if as.LastRunAt != nil {
			touch(*as.LastRunAt)
		}
		touch(a.UpdatedAt)
		if !detail {
			continue
		}
		pa := projectAutomation{
			ID: a.ID, Name: a.Name, Enabled: a.Enabled, TriggerType: string(a.TriggerType), TriggerValue: a.TriggerValue,
			ProjectPath: a.ProjectPath, CreatedBySessionID: a.CreatedBySessionID, State: as.State,
			LastRunAt: as.LastRunAt, NextFireAt: as.NextFireAt, CreatedAt: a.CreatedAt, RecentRuns: as.RecentRuns,
		}
		if loops {
			n := as.PendingTasks
			pa.PendingTasks = &n
			pa.StoppedReason = as.StoppedReason
			if rep, err := s.automations.LatestReport(a.ID); err == nil {
				pa.LatestReport = rep
			}
		}
		pd.AutomationList = append(pd.AutomationList, pa)
	}
	if !last.IsZero() {
		sum.LastUpdatedAt = &last
	}
	pd.projectSummary = sum
	return pd, nil
}

func projectsDisabled(w http.ResponseWriter) bool {
	if projectsEnabled() {
		return false
	}
	writeError(w, http.StatusNotFound, "experimental feature \""+featureProjects+"\" is disabled")
	return true
}

// listProjects returns every project with a summary of its members.
func (s *Server) listProjects(w http.ResponseWriter, r *http.Request) {
	if projectsDisabled(w) {
		return
	}
	m, err := s.loadProjectModel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	projects := m.projects()
	s.persistCoordinators(m, projects)
	now := time.Now()
	out := make([]projectSummary, 0, len(projects))
	for _, d := range projects {
		pd, err := s.buildProject(m, d, false, now)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out = append(out, pd.projectSummary)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].LastUpdatedAt, out[j].LastUpdatedAt
		switch {
		case a != nil && b != nil && !a.Equal(*b):
			return a.After(*b)
		case (a == nil) != (b == nil):
			return a != nil
		}
		return out[i].Path < out[j].Path
	})
	writeJSON(w, http.StatusOK, out)
}

func projectPathParam(r *http.Request) (string, bool) {
	p, err := url.PathUnescape(chi.URLParam(r, "path"))
	if err != nil || p == "" {
		return "", false
	}
	if p[0] != '/' {
		p = "/" + p
	}
	return filepath.Clean(p), true
}

// getProject returns one project's members in detail.
func (s *Server) getProject(w http.ResponseWriter, r *http.Request) {
	if projectsDisabled(w) {
		return
	}
	path, ok := projectPathParam(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid project path")
		return
	}
	m, err := s.loadProjectModel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	projects := m.projects()
	d, found := projects[path]
	if !found {
		writeError(w, http.StatusNotFound, "project not found: "+path)
		return
	}
	s.persistCoordinators(m, projects)
	pd, err := s.buildProject(m, d, true, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pd)
}

// setProjectCoordinator reassigns a project's coordinator.
func (s *Server) setProjectCoordinator(w http.ResponseWriter, r *http.Request) {
	if projectsDisabled(w) {
		return
	}
	path, ok := projectPathParam(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid project path")
		return
	}
	var req struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SessionID == "" {
		writeError(w, http.StatusBadRequest, "sessionId is required")
		return
	}
	m, err := s.loadProjectModel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, found := m.projects()[path]; !found {
		writeError(w, http.StatusNotFound, "project not found: "+path)
		return
	}
	sess, found := m.byID[req.SessionID]
	if !found {
		writeError(w, http.StatusNotFound, "session not found: "+req.SessionID)
		return
	}
	if sess.Path != path {
		writeError(w, http.StatusBadRequest, "session does not belong to this project (project_path differs)")
		return
	}
	if !sess.eligibleCoordinator() {
		writeError(w, http.StatusBadRequest, "session cannot be a coordinator (must be a regular, non-archived session; automation, controller, external and ephemeral sessions are excluded)")
		return
	}
	s.upsertCoordinator(path, sess.ID)
	m, err = s.loadProjectModel()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	d, found := m.projects()[path]
	if !found {
		writeError(w, http.StatusNotFound, "project not found: "+path)
		return
	}
	pd, err := s.buildProject(m, d, true, time.Now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, pd)
}
