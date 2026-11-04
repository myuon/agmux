package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeArtifactAPI struct {
	mu       sync.Mutex
	existing string // id the fake knows about
	putFails bool
	calls    []string // "METHOD path body"
}

func (f *fakeArtifactAPI) handler(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path+" "+string(b))
	f.mu.Unlock()
	switch {
	case r.Method == "GET" && r.URL.Path == "/api/artifacts/"+f.existing:
		w.Write([]byte(`{}`))
	case r.Method == "PUT" && r.URL.Path == "/api/artifacts/"+f.existing:
		if f.putFails {
			http.Error(w, `{"error":"boom"}`, 500)
			return
		}
		w.Write([]byte(`{"url":"/artifacts/` + f.existing + `"}`))
	default:
		http.NotFound(w, r)
	}
}

func setupUpdate(t *testing.T, api *fakeArtifactAPI) (*Server, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	id := strings.Repeat("a", 32)
	itemDir := filepath.Join(home, ".agmux", "artifacts", id)
	if err := os.MkdirAll(itemDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(itemDir, "index.html"), []byte("<p>old</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	api.existing = id
	ts := httptest.NewServer(http.HandlerFunc(api.handler))
	t.Cleanup(ts.Close)
	return NewServerForHTTP("sess-2", ts.URL), id, itemDir
}

func writeSrc(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func errText(res interface{}) string {
	c := res.(map[string]interface{})["content"].([]interface{})
	return c[0].(map[string]interface{})["text"].(string)
}

func isErr(res interface{}) bool { return res.(map[string]interface{})["isError"] == true }

func TestPublishArtifactUpdateReplacesContent(t *testing.T) {
	api := &fakeArtifactAPI{}
	s, id, itemDir := setupUpdate(t, api)
	src := writeSrc(t, "a.html", "<p>new</p>")
	// title omitted: allowed when artifact_id is given
	res, rpcErr := s.handlePublishArtifact([]byte(`{"path":"` + src + `","artifact_id":"` + id + `"}`))
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	if isErr(res) {
		t.Fatalf("unexpected error: %s", toolText(t, res))
	}
	text := toolText(t, res)
	if !strings.Contains(text, id) || !strings.Contains(text, "/artifacts/"+id) || !strings.Contains(text, "artifact_id") {
		t.Fatalf("text = %s", text)
	}
	got, _ := os.ReadFile(filepath.Join(itemDir, "index.html"))
	if string(got) != "<p>new</p>" {
		t.Fatalf("content = %s", got)
	}
	es, _ := os.ReadDir(itemDir)
	if len(es) != 1 {
		t.Fatalf("leftover files in %s: %v", itemDir, es)
	}
	last := api.calls[len(api.calls)-1]
	if !strings.HasPrefix(last, "PUT /api/artifacts/"+id) || strings.Contains(last, `"title"`) || !strings.Contains(last, `"sessionId":"sess-2"`) {
		t.Fatalf("PUT call = %s", last)
	}

	// title given: forwarded
	res, _ = s.handlePublishArtifact([]byte(`{"path":"` + src + `","artifact_id":"` + id + `","title":" New T "}`))
	if isErr(res) || !strings.Contains(api.calls[len(api.calls)-1], `"title":"New T"`) {
		t.Fatalf("title not forwarded: %v", api.calls)
	}
}

func TestPublishArtifactUpdateUnknownIDWritesNothing(t *testing.T) {
	api := &fakeArtifactAPI{}
	s, _, _ := setupUpdate(t, api)
	home := os.Getenv("HOME")
	src := writeSrc(t, "a.html", "<p>new</p>")
	unknown := strings.Repeat("f", 32)
	res, rpcErr := s.handlePublishArtifact([]byte(`{"path":"` + src + `","artifact_id":"` + unknown + `"}`))
	if rpcErr != nil || !isErr(res) {
		t.Fatalf("expected error result, got %v %v", res, rpcErr)
	}
	if msg := errText(res); !strings.Contains(msg, "not found") {
		t.Fatalf("unclear error: %s", msg)
	}
	if _, err := os.Stat(filepath.Join(home, ".agmux", "artifacts", unknown)); !os.IsNotExist(err) {
		t.Fatal("unknown id must not create a directory")
	}
	for _, c := range api.calls {
		if strings.HasPrefix(c, "PUT") || strings.HasPrefix(c, "POST") {
			t.Fatalf("no write call expected: %v", api.calls)
		}
	}
}

func TestPublishArtifactUpdateRejectsBadIDsAndSources(t *testing.T) {
	api := &fakeArtifactAPI{}
	s, id, itemDir := setupUpdate(t, api)
	src := writeSrc(t, "a.html", "<p>new</p>")
	txt := writeSrc(t, "a.txt", "x")
	for name, args := range map[string]string{
		"traversal": `{"path":"` + src + `","artifact_id":"../../etc"}`,
		"short":     `{"path":"` + src + `","artifact_id":"abc"}`,
		"wrong ext": `{"path":"` + txt + `","artifact_id":"` + id + `"}`,
		"missing":   `{"path":"/nonexistent/x.html","artifact_id":"` + id + `"}`,
	} {
		res, rpcErr := s.handlePublishArtifact([]byte(args))
		if rpcErr == nil && !isErr(res) {
			t.Errorf("%s: expected error", name)
		}
		got, _ := os.ReadFile(filepath.Join(itemDir, "index.html"))
		if string(got) != "<p>old</p>" {
			t.Errorf("%s: old content damaged: %s", name, got)
		}
	}
	es, _ := os.ReadDir(itemDir)
	if len(es) != 1 {
		t.Fatalf("leftover files: %v", es)
	}
	for _, c := range api.calls {
		if strings.HasPrefix(c, "PUT") {
			t.Fatalf("PUT must not be called: %v", api.calls)
		}
	}
}

func TestPublishArtifactUpdateRollsBackWhenServerFails(t *testing.T) {
	api := &fakeArtifactAPI{putFails: true}
	s, id, itemDir := setupUpdate(t, api)
	src := writeSrc(t, "a.html", "<p>new</p>")
	res, _ := s.handlePublishArtifact([]byte(`{"path":"` + src + `","artifact_id":"` + id + `"}`))
	if !isErr(res) {
		t.Fatal("expected error")
	}
	got, _ := os.ReadFile(filepath.Join(itemDir, "index.html"))
	if string(got) != "<p>old</p>" {
		t.Fatalf("content not rolled back: %s", got)
	}
	es, _ := os.ReadDir(itemDir)
	if len(es) != 1 {
		t.Fatalf("leftover files: %v", es)
	}
}

func TestPublishArtifactNewStillRequiresTitle(t *testing.T) {
	api := &fakeArtifactAPI{}
	s, _, _ := setupUpdate(t, api)
	src := writeSrc(t, "a.html", "<p>x</p>")
	if _, rpcErr := s.handlePublishArtifact([]byte(`{"path":"` + src + `"}`)); rpcErr == nil {
		t.Fatal("title must stay required without artifact_id")
	}
}
