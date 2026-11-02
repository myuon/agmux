package mcp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishArtifactCopiesAndRegisters(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	src := filepath.Join(t.TempDir(), "a.html")
	if err := os.WriteFile(src, []byte("<p>x</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	var gotPath, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotPath, gotBody = r.URL.Path, string(b)
		w.Write([]byte(`{"url":"/artifacts/abc"}`))
	}))
	defer ts.Close()
	s := NewServerForHTTP("sess-1", ts.URL)
	res, rpcErr := s.handlePublishArtifact([]byte(`{"path":"` + src + `","title":"T"}`))
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	text := toolText(t, res)
	if !strings.Contains(text, "/artifacts/abc") || !strings.Contains(text, "自己完結") {
		t.Fatalf("text = %s", text)
	}
	if gotPath != "/api/sessions/sess-1/artifacts" || !strings.Contains(gotBody, `"title":"T"`) {
		t.Fatalf("path=%s body=%s", gotPath, gotBody)
	}
	entries, _ := os.ReadDir(filepath.Join(home, ".agmux", "artifacts"))
	if len(entries) != 1 {
		t.Fatalf("expected 1 copied artifact, got %d", len(entries))
	}
}

func TestPublishArtifactRejectsBadInput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := t.TempDir()
	txt := filepath.Join(d, "a.txt")
	os.WriteFile(txt, []byte("x"), 0o644)
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer ts.Close()
	s := NewServerForHTTP("sess-1", ts.URL)
	if _, rpcErr := s.handlePublishArtifact([]byte(`{"path":"` + txt + `","title":" "}`)); rpcErr == nil {
		t.Fatal("empty title should be rejected")
	}
	res, _ := s.handlePublishArtifact([]byte(`{"path":"` + txt + `","title":"T"}`))
	if res.(map[string]interface{})["isError"] != true {
		t.Fatal("wrong extension should be an error result")
	}
	if called {
		t.Fatal("API must not be called for invalid input")
	}
}
