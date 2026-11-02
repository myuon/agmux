package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentBrowserSessionNameIsPerAgmuxSession(t *testing.T) {
	a := agentBrowserSessionName("1111-aaaa")
	b := agentBrowserSessionName("2222-bbbb")
	if a == b || a != "agmux-1111-aaaa" {
		t.Fatalf("names = %q %q", a, b)
	}
	if got := agentBrowserSessionName("x/../y z"); strings.ContainsAny(got, "/ .") {
		t.Fatalf("unsafe chars kept: %q", got)
	}
}

func toolText(t *testing.T, res interface{}) string {
	t.Helper()
	m := res.(map[string]interface{})
	if m["isError"] == true {
		t.Fatalf("tool returned error: %v", m)
	}
	return m["content"].([]interface{})[0].(map[string]interface{})["text"].(string)
}

func TestGetLoggedInBrowserReturnsBoundCommand(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		w.Write([]byte(`{"targetId":"T1","cdpUrl":"ws://127.0.0.1:4399/api/remote-browser/agent-cdp/sess-1","reused":false}`))
	}))
	defer ts.Close()
	s := NewServerForHTTP("sess-1", ts.URL)
	res, rpcErr := s.handleGetLoggedInBrowser(nil)
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	text := toolText(t, res)
	if gotPath != "POST /api/remote-browser/agent-tabs/sess-1" {
		t.Fatalf("request = %s", gotPath)
	}
	want := "agent-browser --session agmux-sess-1 --cdp ws://127.0.0.1:4399/api/remote-browser/agent-cdp/sess-1"
	if !strings.Contains(text, want) {
		t.Fatalf("command form missing: %s", text)
	}
}
