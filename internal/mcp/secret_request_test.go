package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestSecretOutcomes(t *testing.T) {
	cases := []struct {
		name, resp, want, notWant string
	}{
		{"saved", `{"response":"secret_saved","timed_out":false}`, "OPENAI_API_KEY を保存しました（agmux secret exec --env OPENAI_API_KEY -- <cmd> で利用できます）", "[CANCELLED]"},
		{"cancelled", `{"response":"secret_cancelled","timed_out":false}`, "[CANCELLED]", "保存しました"},
		{"timeout", `{"response":"auto","timed_out":true}`, "[TIMED OUT]", "保存しました"},
		{"unknown", `{"response":"something else","timed_out":false}`, "[UNKNOWN]", "something else"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var path string
			var body map[string]any
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.URL.Path
				b, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(b, &body)
				w.Write([]byte(c.resp))
			}))
			defer ts.Close()
			s := NewServerForHTTP("sess-1", ts.URL)
			res, rpcErr := s.handleRequestSecret([]byte(`{"name":"OPENAI_API_KEY","description":"embeddings","timeout_seconds":5}`))
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			text := toolText(t, res)
			if !strings.Contains(text, c.want) || strings.Contains(text, c.notWant) {
				t.Fatalf("text = %s", text)
			}
			if path != "/api/sessions/sess-1/escalate" {
				t.Fatalf("path = %s", path)
			}
			if body["secret_name"] != "OPENAI_API_KEY" || body["secret_description"] != "embeddings" || body["timeout_seconds"] != float64(5) || body["id"] == "" {
				t.Fatalf("body = %v", body)
			}
			if _, ok := body["login_url"]; ok {
				t.Fatalf("unexpected login_url in %v", body)
			}
		})
	}
}

func TestRequestSecretDefaultTimeout(t *testing.T) {
	var body map[string]any
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Write([]byte(`{"response":"secret_saved"}`))
	}))
	defer ts.Close()
	s := NewServerForHTTP("", ts.URL)
	if _, rpcErr := s.handleRequestSecret([]byte(`{"session_id":"abc","name":"X","description":"d"}`)); rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if body["timeout_seconds"] != float64(defaultSecretRequestTimeoutSeconds) {
		t.Fatalf("body = %v", body)
	}
}

func TestRequestSecretRejectsBadInput(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer ts.Close()
	s := NewServerForHTTP("sess-1", ts.URL)
	for _, args := range []string{
		`{"name":"","description":"x"}`,
		`{"name":"lower_case","description":"x"}`,
		`{"name":"BAD-NAME","description":"x"}`,
		`{"name":"OK_NAME","description":"  "}`,
	} {
		if _, rpcErr := s.handleRequestSecret([]byte(args)); rpcErr == nil {
			t.Errorf("accepted %s", args)
		}
	}
	if called {
		t.Fatal("backend was called for rejected input")
	}
}

func TestRequestSecretListed(t *testing.T) {
	s := NewServerForHTTP("sess-1", "http://unused")
	resp := callJSONRPC(t, s, "tools/list", `{}`)
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	data, _ := json.Marshal(resp.Result)
	if !strings.Contains(string(data), `"request_secret"`) {
		t.Fatal("request_secret not in tools/list")
	}
}
