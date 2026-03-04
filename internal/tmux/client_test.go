package tmux

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func cleanupSession(t *testing.T, c *Client, name string) {
	t.Helper()
	_ = c.run("kill-session", "-t", SessionPrefix+name)
}

func TestNewSessionAndHasSession(t *testing.T) {
	c := NewClient()
	name := "test-session-1"
	defer cleanupSession(t, c, name)

	err := c.NewSession(name, "/tmp", "")
	require.NoError(t, err)

	assert.True(t, c.HasSession(name))
}

func TestKillSession(t *testing.T) {
	c := NewClient()
	name := "test-session-2"
	defer cleanupSession(t, c, name)

	err := c.NewSession(name, "/tmp", "")
	require.NoError(t, err)

	err = c.KillSession(name)
	require.NoError(t, err)

	assert.False(t, c.HasSession(name))
}

func TestSendKeysAndCapturePane(t *testing.T) {
	c := NewClient()
	name := "test-session-3"
	sessionName := SessionPrefix + name
	defer cleanupSession(t, c, name)

	err := c.NewSession(name, "/tmp", "")
	require.NoError(t, err)

	err = c.SendKeys(sessionName, "echo hello-agmux-test")
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)

	output, err := c.CapturePane(sessionName, 50)
	require.NoError(t, err)
	assert.True(t, strings.Contains(output, "hello-agmux-test"))
}

func TestListSessions(t *testing.T) {
	c := NewClient()
	name := "test-session-4"
	defer cleanupSession(t, c, name)

	err := c.NewSession(name, "/tmp", "")
	require.NoError(t, err)

	sessions, err := c.ListSessions()
	require.NoError(t, err)

	found := false
	for _, s := range sessions {
		if s.Name == SessionPrefix+name {
			found = true
			break
		}
	}
	assert.True(t, found, "created session should appear in list")
}
