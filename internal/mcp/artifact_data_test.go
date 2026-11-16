package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublishArtifactForwardsDataFiles(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := writeSrc(t, "a.html", "<p>x</p>")
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Write([]byte(`{"url":"/artifacts/abc"}`))
	}))
	defer ts.Close()
	s := NewServerForHTTP("sess-1", ts.URL)

	args, _ := json.Marshal(map[string]interface{}{"path": src, "title": "T", "data_files": map[string]string{"ideas.json": "/p/ideas.json"}})
	text := toolText(t, mustPublish(t, s, args))
	if !strings.Contains(gotBody, `"dataFiles":{"ideas.json":"/p/ideas.json"}`) {
		t.Fatalf("body = %s", gotBody)
	}
	if !strings.Contains(text, `fetch("data/<name>")`) || !strings.Contains(text, "ideas.json") {
		t.Fatalf("text = %s", text)
	}

	// Without data_files nothing is sent.
	args, _ = json.Marshal(map[string]interface{}{"path": src, "title": "T"})
	text = toolText(t, mustPublish(t, s, args))
	if strings.Contains(gotBody, "dataFiles") || strings.Contains(text, "データファイル") {
		t.Fatalf("body = %s text = %s", gotBody, text)
	}
}

func mustPublish(t *testing.T, s *Server, args []byte) interface{} {
	t.Helper()
	res, rpcErr := s.handlePublishArtifact(args)
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	return res
}

func TestPublishArtifactUpdateDataFilesKeepReplaceClear(t *testing.T) {
	api := &fakeArtifactAPI{}
	s, id, _ := setupUpdate(t, api)
	src := writeSrc(t, "a.html", "<p>new</p>")
	put := func(extra string) string {
		t.Helper()
		res := mustPublish(t, s, []byte(`{"path":"`+src+`","artifact_id":"`+id+`"`+extra+`}`))
		if isErr(res) {
			t.Fatalf("error: %s", errText(res))
		}
		return api.calls[len(api.calls)-1]
	}
	if c := put(""); strings.Contains(c, "dataFiles") {
		t.Fatalf("omitted should keep (not send): %s", c)
	}
	if c := put(`,"data_files":null`); strings.Contains(c, "dataFiles") {
		t.Fatalf("null should keep (not send): %s", c)
	}
	if c := put(`,"data_files":{"a.json":"/p/a.json"}`); !strings.Contains(c, `"dataFiles":{"a.json":"/p/a.json"}`) {
		t.Fatalf("replace not forwarded: %s", c)
	}
	if c := put(`,"data_files":{}`); !strings.Contains(c, `"dataFiles":{}`) {
		t.Fatalf("clear not forwarded: %s", c)
	}
}
