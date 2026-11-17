package automation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTimeline_EventsAndFeeder(t *testing.T) {
	s := newTestStore(t)
	mk := func(name string) *Automation {
		a, err := s.Create(CreateParams{Name: name, Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "1h", Enabled: true})
		require.NoError(t, err)
		return a
	}
	target, scanner := mk("fixer"), mk("scanner")
	now := time.Now()

	// scanner run (session still running) that added a task to the fixer.
	sr, err := s.InsertRun(Run{AutomationID: scanner.ID, FiredAt: now.Add(-3 * time.Hour), SessionID: "scan-1", Status: RunSuccess})
	require.NoError(t, err)
	task, err := s.AddTaskFrom(target.ID, "fix A", "", CreatedByAgent, scanner.ID)
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE automation_tasks SET created_at = ? WHERE id = ?`, now.Add(-170*time.Minute), task.ID)
	require.NoError(t, err)

	// fixer run on that task, then done.
	fr, err := s.InsertRun(Run{AutomationID: target.ID, FiredAt: now.Add(-2 * time.Hour), SessionID: "fix-1", Status: RunSuccess})
	require.NoError(t, err)
	_, err = s.SaveReport(fr.ID, map[string]float64{"m": 1}, "done", []ReportLink{{Title: "PR", URL: "https://x/1"}})
	require.NoError(t, err)
	_, err = s.NextTask(target.ID, &fr.ID)
	require.NoError(t, err)
	_, err = s.db.Exec(`UPDATE automation_tasks SET status = 'done', updated_at = ? WHERE id = ?`, now.Add(-100*time.Minute), task.ID)
	require.NoError(t, err)

	// watchdog stop.
	_, err = s.InsertRun(Run{AutomationID: target.ID, FiredAt: now.Add(-time.Hour), Status: RunSkipped, Message: "disabled: 3 consecutive errors"})
	require.NoError(t, err)

	tl, err := s.Timeline(target.ID, time.Time{}, 0)
	require.NoError(t, err)
	var kinds []string
	for _, e := range tl.Events {
		kinds = append(kinds, e.Kind)
	}
	// newest first
	assert.Equal(t, []string{EventDisabled, EventTaskFinished, EventRun, EventTaskAdded, EventRun}, kinds)
	assert.Equal(t, "3 consecutive errors", tl.Events[0].Message)

	own := tl.Events[2]
	assert.Nil(t, own.Feeder)
	require.Len(t, own.RunTasks, 1)
	assert.Equal(t, "done", own.Run.Report.Summary)

	added := tl.Events[3]
	require.NotNil(t, added.Source)
	assert.Equal(t, "scanner", added.Source.Name)
	assert.Equal(t, "scan-1", added.SourceSessionID)

	feeder := tl.Events[4]
	require.NotNil(t, feeder.Feeder)
	assert.Equal(t, sr.ID, feeder.Run.ID)
	assert.Equal(t, []string{"fix A"}, feeder.AddedTasks)

	// paging
	p1, err := s.Timeline(target.ID, time.Time{}, 2)
	require.NoError(t, err)
	require.Len(t, p1.Events, 2)
	require.NotEmpty(t, p1.NextBefore)
	before, err := time.Parse(time.RFC3339Nano, p1.NextBefore)
	require.NoError(t, err)
	p2, err := s.Timeline(target.ID, before, 10)
	require.NoError(t, err)
	assert.Len(t, p2.Events, 3)
	assert.Empty(t, p2.NextBefore)
}
