package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestBrowserLoginOutcomes(t *testing.T) {
	cases := []struct {
		name, resp, want, notWant string
	}{
		{"completed", `{"response":"login_completed","timed_out":false}`, "[COMPLETED]", "TIMED OUT"},
		{"cancelled", `{"response":"login_cancelled","timed_out":false}`, "[CANCELLED]", "[COMPLETED]"},
		{"timeout", `{"response":"auto","timed_out":true}`, "[TIMED OUT]", "[COMPLETED]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				body = string(b)
				w.Write([]byte(c.resp))
			}))
			defer ts.Close()
			s := NewServerForHTTP("sess-1", ts.URL)
			res, rpcErr := s.handleRequestBrowserLogin([]byte(`{"url":"https://example.com/login","reason":"expired","timeout_seconds":5}`))
			if rpcErr != nil {
				t.Fatal(rpcErr)
			}
			text := toolText(t, res)
			if !strings.Contains(text, c.want) || strings.Contains(text, c.notWant) {
				t.Fatalf("text = %s", text)
			}
			if !strings.Contains(body, `"login_url":"https://example.com/login"`) || !strings.Contains(body, `"timeout_seconds":5`) {
				t.Fatalf("body = %s", body)
			}
		})
	}
}

func TestRequestBrowserLoginRejectsBadInput(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer ts.Close()
	s := NewServerForHTTP("sess-1", ts.URL)
	for _, args := range []string{
		`{"url":"javascript:alert(1)","reason":"x"}`,
		`{"url":"file:///etc/passwd","reason":"x"}`,
		`{"url":"","reason":"x"}`,
		`{"url":"https://example.com","reason":""}`,
	} {
		if _, rpcErr := s.handleRequestBrowserLogin([]byte(args)); rpcErr == nil {
			t.Errorf("accepted %s", args)
		}
	}
	if called {
		t.Fatal("backend was called for rejected input")
	}
}
