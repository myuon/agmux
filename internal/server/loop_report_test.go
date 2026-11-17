package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/myuon/agmux/internal/automation"
	"github.com/myuon/agmux/internal/experimental"
)

func TestLoopReportAPI(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newAutomationTestServer(t)

	a, err := s.automations.Create(automation.CreateParams{Name: "l", Prompt: "p", TriggerType: automation.TriggerInterval, TriggerValue: "1h", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.automations.InsertRun(automation.Run{AutomationID: a.ID, FiredAt: time.Now().Add(-time.Minute), SessionID: "sess-a", Status: automation.RunSuccess}); err != nil {
		t.Fatal(err)
	}
	body := `{"metrics":{"pass_rate":0.9},"summary":"done","links":[{"title":"PR","url":"https://x"}]}`

	// off: both endpoints are hidden
	if rec := doJSON(t, s, http.MethodPost, "/api/sessions/sess-a/loop-report", body); rec.Code != http.StatusNotFound {
		t.Fatalf("off: post status = %d", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodGet, "/api/automations/"+a.ID+"/reports", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("off: get status = %d", rec.Code)
	}

	if err := experimental.Set("loops", true); err != nil {
		t.Fatal(err)
	}

	// non-automation session -> error
	if rec := doJSON(t, s, http.MethodPost, "/api/sessions/other/loop-report", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("non-automation: status = %d, body %s", rec.Code, rec.Body.String())
	}

	// save, then overwrite
	if rec := doJSON(t, s, http.MethodPost, "/api/sessions/sess-a/loop-report", `{"metrics":{"pass_rate":0.1},"summary":"old"}`); rec.Code != http.StatusOK {
		t.Fatalf("post status = %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(t, s, http.MethodPost, "/api/sessions/sess-a/loop-report", body); rec.Code != http.StatusOK {
		t.Fatalf("post status = %d, body %s", rec.Code, rec.Body.String())
	}

	rec := doJSON(t, s, http.MethodGet, "/api/automations/"+a.ID+"/reports", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body %s", rec.Code, rec.Body.String())
	}
	var got []automation.RunReport
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Report == nil || got[0].Report.Summary != "done" || got[0].Report.Metrics["pass_rate"] != 0.9 {
		t.Fatalf("unexpected reports: %s", rec.Body.String())
	}
	if got[0].SessionID != "sess-a" || got[0].DurationSeconds <= 0 {
		t.Fatalf("missing session/duration: %s", rec.Body.String())
	}
}
