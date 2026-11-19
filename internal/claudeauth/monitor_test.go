package claudeauth

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMonitor_OffIsNoop(t *testing.T) {
	m := NewMonitor(func() bool { return false })
	calls := 0
	m.SetOnChange(func(State, bool) { calls++ })
	m.Report("s1", "Not logged in")
	assert.False(t, m.Required())
	assert.False(t, m.State().LoginRequired)
	assert.Empty(t, m.State().StoppedSessions)
	assert.Equal(t, 0, calls)
}

func TestMonitor_Transitions(t *testing.T) {
	m := NewMonitor(func() bool { return true })
	var became []bool
	m.SetOnChange(func(_ State, b bool) { became = append(became, b) })

	m.Report("s1", "Not logged in")
	assert.True(t, m.Required())
	m.Report("s2", "Not logged in")
	m.Report("s2", "Not logged in") // duplicate: no change
	assert.Equal(t, []bool{true, false}, became)
	st := m.State()
	assert.Equal(t, "Not logged in", st.Reason)
	assert.Equal(t, []string{"s1", "s2"}, st.StoppedSessions)

	m.Clear()
	assert.False(t, m.Required())
	assert.Equal(t, []string{"s1", "s2"}, m.State().StoppedSessions, "stopped sessions stay for resume")

	m.RemoveStopped("s1")
	assert.Equal(t, []string{"s2"}, m.State().StoppedSessions)
	m.RemoveStopped("")
	assert.Empty(t, m.State().StoppedSessions)
	assert.Len(t, became, 5)
}
