package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/myuon/agmux/internal/artifact"
)

func TestInjectArtifactDataHelperPosition(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"
	tag := string(artifactDataHelperTag(id))
	cases := []struct {
		name, in, want string
	}{
		{"after head", "<!DOCTYPE html><html><head><title>x</title></head><body>b</body></html>",
			"<!DOCTYPE html><html><head>" + tag + "<title>x</title></head><body>b</body></html>"},
		{"head with attrs, uppercase", "<HTML><HEAD lang=\"ja\">\n<meta></HEAD><BODY></BODY>",
			"<HTML><HEAD lang=\"ja\">" + tag + "\n<meta></HEAD><BODY></BODY>"},
		{"header is not head", "<!doctype html><body><header>h</header></body>",
			"<!doctype html>" + tag + "<body><header>h</header></body>"},
		{"head string inside body ignored", "<html><body><pre>&lt;head&gt; <head></pre></body>",
			tag + "<html><body><pre>&lt;head&gt; <head></pre></body>"},
		{"BOM and doctype", "\xef\xbb\xbf  <!DOCTYPE html>\n<p>x</p>",
			"\xef\xbb\xbf  <!DOCTYPE html>" + tag + "\n<p>x</p>"},
		{"BOM and head", "\xef\xbb\xbf<!DOCTYPE html><head></head>",
			"\xef\xbb\xbf<!DOCTYPE html><head>" + tag + "</head>"},
		{"fragment", "<p>page</p>", tag + "<p>page</p>"},
		{"empty", "", tag},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, string(injectArtifactDataHelper([]byte(c.in), id)))
		})
	}
	assert.Contains(t, tag, "/artifacts/"+id+"/data/")
	assert.Contains(t, tag, "window.agmuxData")
	assert.Contains(t, tag, `"agmux:data-request"`)
	assert.Contains(t, tag, "window.agmuxOpenArtifact")
	assert.Contains(t, tag, `"agmux:open-artifact"`)
	assert.NotContains(t, tag, "__AGMUX_ARTIFACT_ID__")
}

func TestRawArtifactInjectsHelperOnlyWithDataFiles(t *testing.T) {
	e := newDataTestServer(t)
	html := "\xef\xbb\xbf<!DOCTYPE html><html><head><title>t</title></head><body><p>page</p></body></html>"

	publishHTML := func(dataFiles map[string]string) string {
		id, w := e.publish(t, "s1", nil)
		require.Equal(t, 200, w.Code)
		// publish() stores a fixed page; replace the stored copy with html.
		p, err := e.store.FilePath(id)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(p, []byte(html), 0o644))
		if dataFiles != nil {
			data, err := artifact.ResolveDataFiles(dataFiles, e.project, e.store.Dir())
			require.NoError(t, err)
			_, err = e.store.Touch(id, "T", data)
			require.NoError(t, err)
		}
		return id
	}

	plain := publishHTML(nil)
	r := e.get("GET", "/artifacts/"+plain+"/raw", nil)
	require.Equal(t, 200, r.Code)
	assert.Equal(t, html, r.Body.String(), "byte-identical without data files")
	assert.NotContains(t, r.Body.String(), "agmuxData")

	f := e.write(t, filepath.Join(e.project, "ideas.md"), "# ideas")
	id := publishHTML(map[string]string{"ideas.md": f})
	r = e.get("GET", "/artifacts/"+id+"/raw", nil)
	require.Equal(t, 200, r.Code)
	body := r.Body.String()
	want := strings.Replace(html, "<head>", "<head>"+string(artifactDataHelperTag(id)), 1)
	assert.Equal(t, want, body)
	assert.Empty(t, r.Header().Get("Content-Length"))
	assert.Empty(t, r.Header().Get("ETag"))

	// Clearing the data files restores the original bytes.
	_, err := e.store.Touch(id, "T", &artifact.DataFiles{})
	require.NoError(t, err)
	r = e.get("GET", "/artifacts/"+id+"/raw", nil)
	assert.Equal(t, html, r.Body.String())
}
