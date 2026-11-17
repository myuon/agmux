package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/experimental"
)

func TestAutomationTimelineAPI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newAutomationTestServer(t)
	a, err := s.automations.Create(automation.CreateParams{Name: "l", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.automations.InsertRun(automation.Run{AutomationID: a.ID, FiredAt: time.Now().Add(-time.Minute), SessionID: "sess-a", Status: automation.RunSuccess}); err != nil {
		t.Fatal(err)
	}
	path := "/api/automations/" + a.ID + "/timeline"
	if rec := doJSON(t, s, http.MethodGet, path, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("off: status = %d", rec.Code)
	}
	if err := experimental.Set("loops", true); err != nil {
		t.Fatal(err)
	}
	rec := doJSON(t, s, http.MethodGet, path+"?limit=10", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var tl automation.Timeline
	if err := json.Unmarshal(rec.Body.Bytes(), &tl); err != nil {
		t.Fatal(err)
	}
	if len(tl.Events) != 1 || tl.Events[0].Kind != automation.EventRun {
		t.Fatalf("unexpected timeline: %s", rec.Body.String())
	}
	if rec := doJSON(t, s, http.MethodGet, path+"?before=bad", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad before: status = %d", rec.Code)
	}
}
