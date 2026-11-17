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

func TestListRunReports_UsageViaCLISessionID(t *testing.T) {
	s := newTestStore(t)
	a, err := s.Create(CreateParams{Name: "loop", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "1h", Enabled: true})
	require.NoError(t, err)
	_, err = s.InsertRun(Run{AutomationID: a.ID, FiredAt: time.Now().Add(-time.Hour), SessionID: "agmux-1", Status: RunSuccess})
	require.NoError(t, err)
	_, err = s.db.Exec(`INSERT INTO sessions (id, name, project_path, cli_session_id) VALUES ('agmux-1', 'n', '/tmp', 'cli-1')`)
	require.NoError(t, err)
	now := time.Now()
	_, err = s.db.Exec(`INSERT INTO otel_metrics (name, value, attributes, session_id, timestamp) VALUES
		('claude_code.cost.usage', 1.5, '{}', 'cli-1', ?),
		('claude_code.token.usage', 100, '{"type":"input"}', 'cli-1', ?),
		('claude_code.token.usage', 40, '{"type":"output"}', 'cli-1', ?),
		('claude_code.cost.usage', 9, '{}', 'other', ?)`, now, now, now, now)
	require.NoError(t, err)

	list, err := s.ListRunReports(a.ID, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, 1.5, list[0].Cost)
	assert.Equal(t, 100.0, list[0].InputTokens)
	assert.Equal(t, 40.0, list[0].OutputTokens)
}

func TestFindRunBySession_NotAutomation(t *testing.T) {
	s := newTestStore(t)
	_, err := s.FindRunBySession("unknown")
	assert.ErrorIs(t, err, ErrNoRunForSession)
	_, err = s.FindRunBySession("")
	assert.ErrorIs(t, err, ErrNoRunForSession)
}
