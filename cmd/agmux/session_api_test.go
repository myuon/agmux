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
