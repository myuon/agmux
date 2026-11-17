package automation

import (
	"sort"
	"strings"
	"time"
)

// Timeline event kinds.
const (
	EventRun          = "run"
	EventTaskAdded    = "task_added"
	EventTaskFinished = "task_finished"
	EventDisabled     = "disabled"
)

const (
	defaultTimelineLimit = 50
	maxTimelineLimit     = 200
	// timelineRunWindow bounds how many runs per automation are scanned.
	timelineRunWindow = 500
	// feederSlack tolerates a task being added slightly after the feeder
	// session stopped.
	feederSlack = time.Minute
)

// TimelineEvent is one entry of a loop's chronological timeline.
type TimelineEvent struct {
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`

	// run
	Run *RunReport `json:"run,omitempty"`
	// RunTasks are the tasks the run worked on.
	RunTasks []Task `json:"runTasks,omitempty"`
	// Feeder is set when the run belongs to a feeder loop (a loop that added
	// tasks to this one); AddedTasks are the titles it added here.
	Feeder     *Ref     `json:"feeder,omitempty"`
	AddedTasks []string `json:"addedTasks,omitempty"`

	// task_added / task_finished
	Task *Task `json:"task,omitempty"`
	// Source is the loop that added the task (task_added).
	Source *Ref `json:"source,omitempty"`
	// SourceSessionID is the session of the feeder run that added the task.
	SourceSessionID string `json:"sourceSessionId,omitempty"`

	// disabled
	Message string `json:"message,omitempty"`
}

// Timeline is a page of events, newest first.
type Timeline struct {
	Events []TimelineEvent `json:"events"`
	// NextBefore is the cursor for the next (older) page; empty when done.
	NextBefore string `json:"nextBefore"`
}

// Timeline builds the chronological event list of an automation: its runs
// and stops, task additions / completions, and the runs of feeder loops that
// added tasks to it. before (zero = none) and limit (<=0 = 50) page the
// newest-first result.
func (s *Store) Timeline(automationID string, before time.Time, limit int) (*Timeline, error) {
	if limit <= 0 {
		limit = defaultTimelineLimit
	}
	if limit > maxTimelineLimit {
		limit = maxTimelineLimit
	}
	tasks, err := s.ListTasks(automationID)
	if err != nil {
		return nil, err
	}
	runs, err := s.ListRunReports(automationID, timelineRunWindow)
	if err != nil {
		return nil, err
	}
	feeders, _, err := s.Relations()
	if err != nil {
		return nil, err
	}

	byRun := map[int64][]Task{}
	for _, t := range tasks {
		if t.RunID != nil {
			byRun[*t.RunID] = append(byRun[*t.RunID], t)
		}
	}

	var events []TimelineEvent
	for i := range runs {
		r := runs[i]
		if r.Status == RunSkipped && strings.HasPrefix(r.Message, "disabled:") {
			events = append(events, TimelineEvent{
				Kind: EventDisabled, At: r.FiredAt,
				Message: strings.TrimSpace(strings.TrimPrefix(r.Message, "disabled:")),
			})
			continue
		}
		events = append(events, TimelineEvent{Kind: EventRun, At: r.FiredAt, Run: &r, RunTasks: byRun[r.ID]})
	}

	// Feeder runs: attribute each task to the feeder run whose period
	// contains the task's created_at.
	feederRuns := map[string][]RunReport{}
	feederRef := map[string]Ref{}
	for _, f := range feeders[automationID] {
		fr, err := s.ListRunReports(f.ID, timelineRunWindow)
		if err != nil {
			return nil, err
		}
		feederRuns[f.ID] = fr
		feederRef[f.ID] = f
	}
	added := map[string]map[int64][]string{} // feeder id -> run id -> titles
	for _, t := range tasks {
		tc := t
		ev := TimelineEvent{Kind: EventTaskAdded, At: t.CreatedAt, Task: &tc}
		if t.SourceAutomationID != "" && t.SourceAutomationID != automationID {
			if ref, ok := feederRef[t.SourceAutomationID]; ok {
				ev.Source = &ref
				if fr := matchFeederRun(feederRuns[ref.ID], t.CreatedAt); fr != nil {
					ev.SourceSessionID = fr.SessionID
					if added[ref.ID] == nil {
						added[ref.ID] = map[int64][]string{}
					}
					added[ref.ID][fr.ID] = append(added[ref.ID][fr.ID], t.Title)
				}
			}
		}
		events = append(events, ev)
		if t.Status == TaskDone || t.Status == TaskFailed {
			events = append(events, TimelineEvent{Kind: EventTaskFinished, At: t.UpdatedAt, Task: &tc})
		}
	}
	for fid, perRun := range added {
		ref := feederRef[fid]
		for _, fr := range feederRuns[fid] {
			titles, ok := perRun[fr.ID]
			if !ok {
				continue
			}
			c := fr
			events = append(events, TimelineEvent{Kind: EventRun, At: fr.FiredAt, Run: &c, Feeder: &ref, AddedTasks: titles})
		}
	}

	sort.SliceStable(events, func(i, j int) bool { return events[i].At.After(events[j].At) })
	if !before.IsZero() {
		k := 0
		for k < len(events) && !events[k].At.Before(before) {
			k++
		}
		events = events[k:]
	}
	tl := &Timeline{Events: events}
	if len(events) > limit {
		tl.Events = events[:limit]
		tl.NextBefore = events[limit-1].At.Format(time.RFC3339Nano)
	}
	if tl.Events == nil {
		tl.Events = []TimelineEvent{}
	}
	return tl, nil
}

// matchFeederRun returns the latest feeder run that started at or before at
// and (when it has stopped) ended no more than feederSlack before it.
func matchFeederRun(runs []RunReport, at time.Time) *RunReport {
	var best *RunReport
	for i := range runs {
		r := &runs[i]
		if r.SessionID == "" || r.FiredAt.After(at) {
			continue
		}
		if r.Stopped && at.After(r.FiredAt.Add(time.Duration(r.DurationSeconds*float64(time.Second))+feederSlack)) {
			continue
		}
		if best == nil || r.FiredAt.After(best.FiredAt) {
			best = r
		}
	}
	return best
}
