package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestAutomationAPIAddTaskSendsSourceSession(t *testing.T) {
	var got map[string]string
	api, srv := newTestAPI(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = map[string]string{}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	t.Setenv("AGMUX_SESSION_ID", "sess-1")
	if _, err := api.addTask("a1", "t", ""); err != nil {
		t.Fatal(err)
	}
	if got["sourceSessionId"] != "sess-1" {
		t.Fatalf("sourceSessionId = %q, want sess-1", got["sourceSessionId"])
	}

	t.Setenv("AGMUX_SESSION_ID", "")
	if _, err := api.addTask("a1", "t", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["sourceSessionId"]; ok {
		t.Fatalf("sourceSessionId must be omitted without AGMUX_SESSION_ID: %v", got)
	}
}
