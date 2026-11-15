package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestParseEnvBinding(t *testing.T) {
	cases := []struct {
		spec, name, env string
		ok              bool
	}{
		{"GITHUB_TOKEN", "GITHUB_TOKEN", "GITHUB_TOKEN", true},
		{"GITHUB_TOKEN=GH_TOKEN", "GITHUB_TOKEN", "GH_TOKEN", true},
		{"API_KEY=api_key", "API_KEY", "api_key", true},
		{"lower", "", "", false},
		{"A=1BAD", "", "", false},
		{"A=", "", "", false},
		{"=A", "", "", false},
	}
	for _, c := range cases {
		n, e, err := parseEnvBinding(c.spec)
		if (err == nil) != c.ok {
			t.Errorf("parseEnvBinding(%q) err = %v, want ok=%v", c.spec, err, c.ok)
			continue
		}
		if c.ok && (n != c.name || e != c.env) {
			t.Errorf("parseEnvBinding(%q) = %q, %q", c.spec, n, e)
		}
	}
}

func TestCommandSummary(t *testing.T) {
	name, n := commandSummary([]string{"/usr/bin/curl", "-H", "Authorization: Bearer x"})
	if name != "curl" || n != 2 {
		t.Fatalf("commandSummary = %q, %d", name, n)
	}
	if name, n := commandSummary(nil); name != "" || n != 0 {
		t.Fatalf("empty commandSummary = %q, %d", name, n)
	}
}

// TestReportUsageOmitsArgs checks the usage report sends session/cwd/exit
// code and the command summary, but never the command's arguments.
func TestReportUsageOmitsArgs(t *testing.T) {
	sensitive := "tok" + strings.Repeat("Q7", 10)
	type hit struct {
		path string
		body map[string]any
		raw  string
	}
	var (
		mu   sync.Mutex
		hits []hit
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		mu.Lock()
		hits = append(hits, hit{r.URL.Path, m, string(raw)})
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	api := &secretAPI{baseURL: srv.URL, client: srv.Client()}
	args := []string{"/opt/bin/mytool", "--token", sensitive, "push"}
	api.reportUsage(context.Background(), []string{"A", "B"}, newSecretUsageReport(args, "sess-1", "/work/proj", 7))

	if len(hits) != 2 || hits[0].path != "/api/secrets/A/used" || hits[1].path != "/api/secrets/B/used" {
		t.Fatalf("unexpected requests %+v", hits)
	}
	want := map[string]any{"sessionId": "sess-1", "cwd": "/work/proj", "command": "mytool", "argCount": float64(3), "exitCode": float64(7)}
	for _, h := range hits {
		if strings.Contains(h.raw, sensitive) || strings.Contains(h.raw, "--token") || strings.Contains(h.raw, "/opt/bin") {
			t.Fatalf("report leaks arguments: %s", h.raw)
		}
		for k, v := range want {
			if h.body[k] != v {
				t.Errorf("%s = %v, want %v", k, h.body[k], v)
			}
		}
	}
}
