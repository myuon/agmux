package automation

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/db"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func strPtr(s string) *string { return &s }

func TestStore_ModelRoundTrip(t *testing.T) {
	store := newTestStore(t)
	base := CreateParams{Name: "n", Prompt: "p", TriggerType: TriggerInterval, TriggerValue: "1h", Enabled: true}

	noModel, err := store.Create(base)
	require.NoError(t, err)
	assert.Equal(t, "", noModel.Model)

	withModel := base
	withModel.Model = "  claude-haiku-5-5 "
	created, err := store.Create(withModel)
	require.NoError(t, err)
	assert.Equal(t, "claude-haiku-5-5", created.Model)

	got, err := store.Get(created.ID)
	require.NoError(t, err)
	assert.Equal(t, "claude-haiku-5-5", got.Model)

	list, err := store.List()
	require.NoError(t, err)
	models := map[string]string{}
	for _, a := range list {
		models[a.ID] = a.Model
	}
	assert.Equal(t, "claude-haiku-5-5", models[created.ID])
	assert.Equal(t, "", models[noModel.ID])
	enabled, err := store.ListEnabled()
	require.NoError(t, err)
	assert.Len(t, enabled, 2)
}

func TestStore_UpdateModel(t *testing.T) {
	cases := []struct {
		name  string
		model *string
		want  string
	}{
		{"omitted keeps", nil, "claude-haiku-5-5"},
		{"explicit empty clears", strPtr(""), ""},
		{"whitespace clears", strPtr("   "), ""},
		{"value replaces", strPtr(" other-model "), "other-model"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			a, err := store.Create(CreateParams{Name: "n", Prompt: "p", TriggerType: TriggerInterval,
				TriggerValue: "1h", Model: "claude-haiku-5-5", Enabled: true})
			require.NoError(t, err)

			updated, err := store.Update(a.ID, UpdateParams{Name: "n2", Prompt: "p", TriggerType: TriggerInterval,
				TriggerValue: "2h", Model: tc.model, Enabled: true})
			require.NoError(t, err)
			assert.Equal(t, tc.want, updated.Model)
			assert.Equal(t, "n2", updated.Name)
		})
	}
}

func TestStore_SetEnabledKeepsModel(t *testing.T) {
	store := newTestStore(t)
	a, err := store.Create(CreateParams{Name: "n", Prompt: "p", TriggerType: TriggerInterval,
		TriggerValue: "1h", Model: "m1", Enabled: true})
	require.NoError(t, err)
	require.NoError(t, store.SetEnabled(a.ID, false))
	got, err := store.Get(a.ID)
	require.NoError(t, err)
	assert.Equal(t, "m1", got.Model)
	assert.False(t, got.Enabled)
}

// A DB created before the model column existed must upgrade cleanly and keep
// its rows (with an empty model).
func TestMigration_AddsModelColumnToExistingDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite3", path)
	require.NoError(t, err)
	_, err = old.Exec(`
		CREATE TABLE automations (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			prompt TEXT NOT NULL,
			trigger_type TEXT NOT NULL,
			trigger_value TEXT NOT NULL,
			project_path TEXT,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`)
	require.NoError(t, err)
	_, err = old.Exec(`INSERT INTO automations (id, name, prompt, trigger_type, trigger_value, project_path, enabled, created_at, updated_at)
		VALUES ('old1', 'legacy', 'p', 'interval', '1h', NULL, 1, ?, ?)`, time.Now(), time.Now())
	require.NoError(t, err)
	require.NoError(t, old.Close())

	// Open twice: the second run must tolerate the already-added column.
	for i := 0; i < 2; i++ {
		d, err := db.Open(path)
		require.NoError(t, err)
		store := NewStore(d)
		a, err := store.Get("old1")
		require.NoError(t, err)
		assert.Equal(t, "legacy", a.Name)
		assert.Equal(t, "", a.Model)
		require.NoError(t, d.Close())
	}
}

func TestRunner_PassesModel(t *testing.T) {
	store := newTestStore(t)
	sessions := newFakeSessions()
	r := newTestRunner(t, store, sessions)

	for _, model := range []string{"claude-haiku-5-5", ""} {
		a, err := store.Create(CreateParams{Name: "a", Prompt: "p", TriggerType: TriggerInterval,
			TriggerValue: "30m", ProjectPath: "/tmp/proj", Model: model, Enabled: true})
		require.NoError(t, err)
		run := r.Run(*a, time.Now())
		require.Equal(t, RunSuccess, run.Status)
		assert.Equal(t, model, sessions.created[len(sessions.created)-1].opts.Model)
	}
}
