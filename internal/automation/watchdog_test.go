package automation

import (
	"testing"
	"time"

	"github.com/myuon/agmux/internal/experimental"
	"github.com/myuon/agmux/internal/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wdEnv struct {
	store    *Store
	sessions *fakeSessions
	sched    *Scheduler
	notes    *[]string
	a        *Automation
}

func newWDEnv(t *testing.T, mod func(*CreateParams)) *wdEnv {
	t.Helper()
	enableLoops(t)
	store := newTestStore(t)
	sessions := newFakeSessions()
	sched := newTestScheduler(t, store, sessions)
	notes := &[]string{}
	sched.Notify = func(a Automation, sessionID, message string) { *notes = append(*notes, message) }
	p := CreateParams{Name: "wd", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "1000h", Enabled: true}
	if mod != nil {
		mod(&p)
	}
	a, err := store.Create(p)
	require.NoError(t, err)
	return &wdEnv{store: store, sessions: sessions, sched: sched, notes: notes, a: a}
}

func (e *wdEnv) addRun(t *testing.T, status RunStatus, session string, pending int, metrics map[string]float64, report bool) int64 {
	t.Helper()
	run := Run{AutomationID: e.a.ID, FiredAt: time.Now(), Status: status, SessionID: session, PendingAtFire: &pending}
	if status == RunError {
		run.Message = "boom"
	}
	saved, err := e.store.InsertRun(run)
	require.NoError(t, err)
	if report {
		_, err = e.store.SaveReport(saved.ID, metrics, "s", nil)
		require.NoError(t, err)
	}
	return saved.ID
}

func (e *wdEnv) enabled(t *testing.T) bool {
	t.Helper()
	got, err := e.store.Get(e.a.ID)
	require.NoError(t, err)
	return got.Enabled
}

func TestWatchdog_ConsecutiveErrorsDisableAndNotifyOnce(t *testing.T) {
	e := newWDEnv(t, nil)
	e.addRun(t, RunError, "", 0, nil, false)
	e.addRun(t, RunError, "", 0, nil, false)
	e.sched.Tick(time.Now())
	assert.True(t, e.enabled(t), "2 errors are not enough")
	assert.Empty(t, *e.notes)

	e.addRun(t, RunError, "", 0, nil, false)
	e.sched.Tick(time.Now())
	assert.False(t, e.enabled(t))
	require.Len(t, *e.notes, 1)
	runs, err := e.store.ListRuns(e.a.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, RunSkipped, runs[0].Status)
	assert.Contains(t, runs[0].Message, "disabled: 3 consecutive errors")

	// Re-enabled: the old errors are behind the marker, so it is not disabled again.
	require.NoError(t, e.store.SetEnabled(e.a.ID, true))
	e.sched.Tick(time.Now())
	assert.True(t, e.enabled(t))
	assert.Len(t, *e.notes, 1)
}

func TestWatchdog_ErrorsInterruptedBySuccessDoNotTrigger(t *testing.T) {
	e := newWDEnv(t, nil)
	e.addRun(t, RunError, "", 0, nil, false)
	e.addRun(t, RunSuccess, "s1", 0, nil, true)
	e.addRun(t, RunError, "", 0, nil, false)
	e.sched.Tick(time.Now())
	assert.True(t, e.enabled(t))
	assert.Empty(t, *e.notes)
}

func TestWatchdog_SkippedRunsAreIgnored(t *testing.T) {
	e := newWDEnv(t, nil)
	e.addRun(t, RunError, "", 0, nil, false)
	_, err := e.store.InsertRun(Run{AutomationID: e.a.ID, FiredAt: time.Now(), Status: RunSkipped, Message: "still active"})
	require.NoError(t, err)
	e.addRun(t, RunError, "", 0, nil, false)
	e.addRun(t, RunError, "", 0, nil, false)
	e.sched.Tick(time.Now())
	assert.False(t, e.enabled(t))
}

func (e *wdEnv) finishedSession(id string) {
	e.sessions.mu.Lock()
	e.sessions.statuses[id] = session.StatusIdle
	e.sessions.mu.Unlock()
}

func TestWatchdog_NoReport_NotifyOnlyForNonQueue(t *testing.T) {
	e := newWDEnv(t, nil)
	// has been used as a loop before (recorded a report once)
	e.finishedSession("old")
	e.addRun(t, RunSuccess, "old", 0, map[string]float64{"x": 1}, true)
	for _, id := range []string{"a", "b", "c"} {
		e.finishedSession(id)
		e.addRun(t, RunSuccess, id, 0, nil, false)
	}
	e.sched.Tick(time.Now())
	assert.True(t, e.enabled(t))
	require.Len(t, *e.notes, 1)
	assert.Contains(t, (*e.notes)[0], "loop_report")

	// no duplicate on the next tick
	e.sched.Tick(time.Now())
	assert.Len(t, *e.notes, 1)
}

func TestWatchdog_NoReport_QueueDisables(t *testing.T) {
	e := newWDEnv(t, func(p *CreateParams) { p.TriggerType = TriggerQueue; p.TriggerValue = "" })
	for _, id := range []string{"a", "b", "c"} {
		e.finishedSession(id)
		e.addRun(t, RunSuccess, id, 1, nil, false)
	}
	e.sched.Tick(time.Now())
	assert.False(t, e.enabled(t))
	assert.Len(t, *e.notes, 1)
}

func TestWatchdog_NoReport_ActiveOrReportedSessionsDoNotTrigger(t *testing.T) {
	e := newWDEnv(t, nil)
	e.finishedSession("a")
	e.addRun(t, RunSuccess, "a", 0, nil, false)
	e.finishedSession("b")
	e.addRun(t, RunSuccess, "b", 0, map[string]float64{"x": 1}, true)
	e.sessions.setStatus("c", session.StatusWorking) // still running
	e.addRun(t, RunSuccess, "c", 0, nil, false)
	e.sched.Tick(time.Now())
	assert.Empty(t, *e.notes)
}

func TestWatchdog_PendingGrowth(t *testing.T) {
	e := newWDEnv(t, nil)
	// needs 3 strict increases = 4 runs
	for i, p := range []int{1, 2, 3} {
		e.finishedSession(string(rune('a' + i)))
		e.addRun(t, RunSuccess, string(rune('a'+i)), p, map[string]float64{"x": 1}, true)
	}
	e.sched.Tick(time.Now())
	assert.Empty(t, *e.notes)

	e.finishedSession("d")
	e.addRun(t, RunSuccess, "d", 4, map[string]float64{"x": 1}, true)
	e.sched.Tick(time.Now())
	require.Len(t, *e.notes, 1)
	assert.Contains(t, (*e.notes)[0], "増え続けて")
	assert.True(t, e.enabled(t), "notify only")

	e.sched.Tick(time.Now())
	assert.Len(t, *e.notes, 1, "no duplicate")

	// A non-increasing run clears the condition; a new growth would notify again.
	e.finishedSession("e")
	e.addRun(t, RunSuccess, "e", 4, map[string]float64{"x": 1}, true)
	e.sched.Tick(time.Now())
	assert.Len(t, *e.notes, 1)
}

func TestWatchdog_PendingUnknownDoesNotTrigger(t *testing.T) {
	e := newWDEnv(t, nil)
	for i, p := range []int{-1, 2, 3, 4} {
		id := string(rune('a' + i))
		e.finishedSession(id)
		e.addRun(t, RunSuccess, id, p, map[string]float64{"x": 1}, true)
	}
	e.sched.Tick(time.Now())
	assert.Empty(t, *e.notes)
}

func TestWatchdog_Stagnation(t *testing.T) {
	cases := []struct {
		name   string
		dir    WatchDirection
		values []float64
		notify bool
	}{
		{"higher stalled", WatchHigher, []float64{5, 9, 8, 9, 7}, true},
		{"higher improved", WatchHigher, []float64{5, 9, 8, 9, 10}, false},
		{"lower stalled", WatchLower, []float64{5, 2, 3, 2, 4}, true},
		{"lower improved", WatchLower, []float64{5, 2, 3, 2, 1}, false},
		{"not enough history", WatchHigher, []float64{5, 4, 3}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newWDEnv(t, func(p *CreateParams) { p.WatchMetric = "score"; p.WatchDirection = tc.dir })
			for i, v := range tc.values {
				id := string(rune('a' + i))
				e.finishedSession(id)
				e.addRun(t, RunSuccess, id, 0, map[string]float64{"score": v}, true)
			}
			e.sched.Tick(time.Now())
			if tc.notify {
				require.Len(t, *e.notes, 1)
				assert.Contains(t, (*e.notes)[0], "score")
				e.sched.Tick(time.Now())
				assert.Len(t, *e.notes, 1, "no duplicate")
			} else {
				assert.Empty(t, *e.notes)
			}
			assert.True(t, e.enabled(t))
		})
	}
}

func TestWatchdog_NoMetricConfiguredNoStagnation(t *testing.T) {
	e := newWDEnv(t, nil)
	for i, v := range []float64{9, 1, 1, 1, 1} {
		id := string(rune('a' + i))
		e.finishedSession(id)
		e.addRun(t, RunSuccess, id, 0, map[string]float64{"score": v}, true)
	}
	e.sched.Tick(time.Now())
	assert.Empty(t, *e.notes)
}

func TestWatchdog_LoopsOffDoesNothing(t *testing.T) {
	e := newWDEnv(t, nil)
	for i := 0; i < 3; i++ {
		e.addRun(t, RunError, "", 0, nil, false)
	}
	require.NoError(t, experimental.Set(FeatureLoops, false))
	e.sched.Tick(time.Now())
	assert.True(t, e.enabled(t))
	assert.Empty(t, *e.notes)
}

func TestRunner_RecordsPendingAtFire(t *testing.T) {
	e := newWDEnv(t, nil)
	_, err := e.store.AddTask(e.a.ID, "t1", "", "user")
	require.NoError(t, err)
	_, err = e.store.AddTask(e.a.ID, "t2", "", "user")
	require.NoError(t, err)
	runner := newTestRunner(t, e.store, e.sessions)
	runner.Run(*e.a, time.Now())
	runs, err := e.store.WatchRuns(e.a.ID, 1)
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, 2, runs[0].Pending)
}

func TestStore_WatchSettings(t *testing.T) {
	store := newTestStore(t)
	base := CreateParams{Name: "n", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "1h", Enabled: true}

	bad := base
	bad.WatchMetric = "x"
	_, err := store.Create(bad)
	assert.Error(t, err, "direction required")

	ok := base
	ok.WatchMetric = "failing_tests"
	ok.WatchDirection = WatchLower
	a, err := store.Create(ok)
	require.NoError(t, err)
	got, err := store.Get(a.ID)
	require.NoError(t, err)
	assert.Equal(t, "failing_tests", got.WatchMetric)
	assert.Equal(t, WatchLower, got.WatchDirection)

	up := UpdateParams{Name: "n", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "1h", Enabled: true}
	kept, err := store.Update(a.ID, up)
	require.NoError(t, err)
	assert.Equal(t, "failing_tests", kept.WatchMetric, "omitted keeps")

	up.WatchMetric = strPtr("")
	cleared, err := store.Update(a.ID, up)
	require.NoError(t, err)
	assert.Equal(t, "", cleared.WatchMetric)
	assert.Equal(t, WatchDirection(""), cleared.WatchDirection)
}

func TestWatchdog_NoReport_NonLoopAutomationIsSilent(t *testing.T) {
	e := newWDEnv(t, nil) // interval trigger, never reported
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		e.finishedSession(id)
		e.addRun(t, RunSuccess, id, 0, nil, false)
		e.sched.Tick(time.Now())
	}
	assert.Empty(t, *e.notes)
	assert.True(t, e.enabled(t))
}

func TestWatchdog_NoReport_OncePerStateAndRearms(t *testing.T) {
	e := newWDEnv(t, nil)
	e.finishedSession("old")
	e.addRun(t, RunSuccess, "old", 0, map[string]float64{"x": 1}, true)
	for _, id := range []string{"a", "b", "c"} {
		e.finishedSession(id)
		e.addRun(t, RunSuccess, id, 0, nil, false)
	}
	e.sched.Tick(time.Now())
	require.Len(t, *e.notes, 1)

	// more runs while the state continues: no re-notification
	for _, id := range []string{"d", "e"} {
		e.finishedSession(id)
		e.addRun(t, RunSuccess, id, 0, nil, false)
		e.sched.Tick(time.Now())
	}
	assert.Len(t, *e.notes, 1)

	// a report leaves the state, then three silent runs enter it again
	e.finishedSession("r")
	e.addRun(t, RunSuccess, "r", 0, map[string]float64{"x": 1}, true)
	e.sched.Tick(time.Now())
	for _, id := range []string{"f", "g", "h"} {
		e.finishedSession(id)
		e.addRun(t, RunSuccess, id, 0, nil, false)
	}
	e.sched.Tick(time.Now())
	assert.Len(t, *e.notes, 2)
}

func TestWatchdog_Stagnation_OncePerBestValue(t *testing.T) {
	e := newWDEnv(t, func(p *CreateParams) { p.WatchMetric = "score"; p.WatchDirection = WatchHigher })
	add := func(id string, v float64) {
		e.finishedSession(id)
		e.addRun(t, RunSuccess, id, 0, map[string]float64{"score": v}, true)
		e.sched.Tick(time.Now())
	}
	for i, v := range []float64{5, 9, 8, 9, 7} {
		add(string(rune('a'+i)), v)
	}
	require.Len(t, *e.notes, 1)
	add("f", 6)
	add("g", 7)
	assert.Len(t, *e.notes, 1, "same best value: no repeat")

	// improvement clears the state; a new stall at a new best notifies again
	add("h", 12)
	for i, v := range []float64{1, 2, 3} {
		add(string(rune('i'+i)), v)
	}
	assert.Len(t, *e.notes, 2)
	assert.Contains(t, (*e.notes)[1], "12")
}
