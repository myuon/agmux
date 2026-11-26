package main

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendViaDaemon_PostsToSendEndpoint(t *testing.T) {
	var gotPath, gotText string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotText = body.Text
		_, _ = w.Write([]byte(`{"status":"sent"}`))
	}))
	defer srv.Close()

	if err := sendViaDaemon(srv.URL, "abcde", "hello"); err != nil {
		t.Fatalf("sendViaDaemon: %v", err)
	}
	if gotPath != "/api/sessions/abcde/send" || gotText != "hello" {
		t.Errorf("got path=%q text=%q", gotPath, gotText)
	}
}

func TestSendViaDaemon_ReturnsDaemonError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"session abcde is being deleted"}`))
	}))
	defer srv.Close()

	err := sendViaDaemon(srv.URL, "abcde", "hello")
	if err == nil || err.Error() != "session abcde is being deleted" {
		t.Fatalf("err = %v, want daemon error message", err)
	}
	if errors.Is(err, errServerUnreachable) {
		t.Error("daemon error must not be treated as unreachable (would fall back to in-process send)")
	}
}

func TestSendViaDaemon_ConnectionRefusedIsUnreachable(t *testing.T) {
	// Grab a free port and close it so nothing is listening.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	err = sendViaDaemon("http://"+addr, "abcde", "hello")
	if !errors.Is(err, errServerUnreachable) {
		t.Fatalf("err = %v, want errServerUnreachable", err)
	}
}

func TestBroadcastViaDaemon_DecodesResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sessions/broadcast" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"results":[{"sessionId":"a","status":"sent"},{"sessionId":"b","status":"error","error":"boom"}]}`))
	}))
	defer srv.Close()

	res, err := broadcastViaDaemon(srv.URL, "hi", "active")
	if err != nil {
		t.Fatalf("broadcastViaDaemon: %v", err)
	}
	if len(res) != 2 || res[1].Error != "boom" {
		t.Errorf("results = %+v", res)
	}
}

// The mm-live-alert watcher runs
// `agmux session create mm-live-alert --ephemeral --timeout 300 --model ... -p <dir> -m <prompt>`.
// That must reach the daemon's POST /api/sessions with the field names the
// daemon decodes (server.createSessionRequest), so the daemon owns the
// holder connection and turns the session idle on the result event.
func TestCreateViaDaemon_PostsCreateRequest(t *testing.T) {
	var gotPath string
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"qdhYx","name":"mm-live-alert","status":"working","type":"ephemeral"}`))
	}))
	defer srv.Close()

	timeout := 300
	ref, err := createViaDaemon(srv.URL, daemonCreateRequest{
		Name:                    "mm-live-alert",
		ProjectPath:             "/tmp/crypto-agent-lab",
		Prompt:                  "notify once and exit",
		Provider:                "claude",
		Model:                   "claude-haiku-5-5",
		AutoApprove:             true,
		Ephemeral:               true,
		EphemeralTimeoutSeconds: &timeout,
		CreatedBySessionID:      "eFvsn",
	})
	if err != nil {
		t.Fatalf("createViaDaemon: %v", err)
	}
	if ref.ID != "qdhYx" || ref.Name != "mm-live-alert" {
		t.Errorf("ref = %+v", ref)
	}
	if gotPath != "/api/sessions" {
		t.Errorf("path = %q, want /api/sessions", gotPath)
	}
	want := map[string]interface{}{
		"name":                    "mm-live-alert",
		"projectPath":             "/tmp/crypto-agent-lab",
		"prompt":                  "notify once and exit",
		"provider":                "claude",
		"model":                   "claude-haiku-5-5",
		"autoApprove":             true,
		"ephemeral":               true,
		"ephemeralTimeoutSeconds": float64(300),
		"createdBySessionId":      "eFvsn",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("body[%q] = %v, want %v", k, got[k], v)
		}
	}
}

func TestCreateViaDaemon_ConnectionRefusedIsUnreachable(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	_, err = createViaDaemon("http://"+addr, daemonCreateRequest{Name: "x", ProjectPath: "/tmp"})
	if !errors.Is(err, errServerUnreachable) {
		t.Fatalf("err = %v, want errServerUnreachable", err)
	}
}

func TestCreateViaDaemon_ReturnsDaemonError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"start holder: boom"}`))
	}))
	defer srv.Close()

	_, err := createViaDaemon(srv.URL, daemonCreateRequest{Name: "x", ProjectPath: "/tmp"})
	if err == nil || err.Error() != "start holder: boom" {
		t.Fatalf("err = %v, want daemon error message", err)
	}
	if errors.Is(err, errServerUnreachable) {
		t.Error("daemon error must not fall back to in-process create (would create a second session)")
	}
}

func TestForkViaDaemon_PostsForkRequest(t *testing.T) {
	var gotPath string
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"new01","name":"orig (fork)"}`))
	}))
	defer srv.Close()

	ref, err := forkViaDaemon(srv.URL, "abcde", false, "continue")
	if err != nil {
		t.Fatalf("forkViaDaemon: %v", err)
	}
	if gotPath != "/api/sessions/abcde/fork" || got["preserveContext"] != false || got["prompt"] != "continue" {
		t.Errorf("path=%q body=%v", gotPath, got)
	}
	if ref.ID != "new01" {
		t.Errorf("ref = %+v", ref)
	}
}

func TestLifecycleViaDaemon_PostsToEndpoints(t *testing.T) {
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		paths = append(paths, r.URL.Path)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	if err := stopViaDaemon(srv.URL, "abcde"); err != nil {
		t.Fatalf("stopViaDaemon: %v", err)
	}
	if err := archiveViaDaemon(srv.URL, "abcde"); err != nil {
		t.Fatalf("archiveViaDaemon: %v", err)
	}
	if err := restartViaDaemon(srv.URL, "abcde"); err != nil {
		t.Fatalf("restartViaDaemon: %v", err)
	}
	want := []string{"/api/sessions/abcde/stop", "/api/sessions/abcde/archive", "/api/sessions/abcde/restart"}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Errorf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}

func TestArchiveViaDaemon_NotFoundIsNotUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"session not found"}`))
	}))
	defer srv.Close()

	err := archiveViaDaemon(srv.URL, "nope")
	if err == nil || err.Error() != "session not found" {
		t.Fatalf("err = %v", err)
	}
	if errors.Is(err, errServerUnreachable) {
		t.Error("daemon error must not fall back to in-process archive")
	}
}
