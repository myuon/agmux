package automation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReport_SaveOverwriteAndList(t *testing.T) {
	s := newTestStore(t)
	a, err := s.Create(CreateParams{Name: "loop", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "1h", Enabled: true})
	require.NoError(t, err)

	r1, err := s.InsertRun(Run{AutomationID: a.ID, FiredAt: time.Now().Add(-2 * time.Hour), SessionID: "sess-1", Status: RunSuccess})
	require.NoError(t, err)
	r2, err := s.InsertRun(Run{AutomationID: a.ID, FiredAt: time.Now().Add(-1 * time.Hour), SessionID: "sess-2", Status: RunSuccess})
	require.NoError(t, err)

	found, err := s.FindRunBySession("sess-1")
	require.NoError(t, err)
	assert.Equal(t, r1.ID, found.ID)

	_, err = s.SaveReport(r1.ID, map[string]float64{"pass_rate": 0.5}, "first", nil)
	require.NoError(t, err)
	_, err = s.SaveReport(r1.ID, map[string]float64{"pass_rate": 0.8}, "second", []ReportLink{{Title: "PR", URL: "https://example.com/1"}})
	require.NoError(t, err)

	list, err := s.ListRunReports(a.ID, 0)
	require.NoError(t, err)
	require.Len(t, list, 2)
	// chronological: oldest first
	assert.Equal(t, r1.ID, list[0].ID)
	assert.Equal(t, r2.ID, list[1].ID)
	require.NotNil(t, list[0].Report)
	assert.Equal(t, "second", list[0].Report.Summary)
	assert.Equal(t, 0.8, list[0].Report.Metrics["pass_rate"])
	require.Len(t, list[0].Report.Links, 1)
	assert.Nil(t, list[1].Report)
	assert.Equal(t, "sess-1", list[0].SessionID)
	assert.Greater(t, list[0].DurationSeconds, 0.0)
}

func TestFindRunBySession_NotAutomation(t *testing.T) {
	s := newTestStore(t)
	_, err := s.FindRunBySession("unknown")
	assert.ErrorIs(t, err, ErrNoRunForSession)
	_, err = s.FindRunBySession("")
	assert.ErrorIs(t, err, ErrNoRunForSession)
}
