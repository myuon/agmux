package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/myuon/agmux/internal/experimental"
)

func TestGetProjectStatusGatedByProjects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tools := []interface{}{getProjectStatusToolDef()}
	if got := filterExperimentalTools(tools); len(got) != 0 {
		t.Fatal("get_project_status must be hidden while projects is off")
	}
	s := NewServerForHTTP("sess", "http://127.0.0.1:0")
	if _, err := s.callTool("get_project_status", nil); err == nil {
		t.Fatal("expected call to be rejected while off")
	}
	if err := experimental.Set("projects", true); err != nil {
		t.Fatal(err)
	}
	if got := filterExperimentalTools(tools); len(got) != 1 {
		t.Fatal("get_project_status must be visible when projects is on")
	}
}

func TestGetProjectStatusReturnsText(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := experimental.Set("projects", true); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sessions/sess/project-status" {
			t.Errorf("path = %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"text": "プロジェクト: repo"})
	}))
	defer ts.Close()
	s := NewServerForHTTP("sess", ts.URL)
	res, err := s.callTool("get_project_status", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	if !strings.Contains(string(b), "プロジェクト: repo") {
		t.Fatalf("res = %s", b)
	}
}
