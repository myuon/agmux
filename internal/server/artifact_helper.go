package server

import (
	"bytes"
	"regexp"
	"strings"
)

// artifactDataHelperJS defines window.agmuxData(name) inside artifacts that
// have data files. Behind an authenticating proxy (e.g. Cloudflare Access) a
// fetch from the sandboxed, opaque-origin document is cross-site, so the auth
// cookie is not sent and the request is redirected to a login page. The
// helper therefore asks the parent agmux page (same-origin, has the cookie) to
// fetch the file and post the result back:
//
//	iframe → parent: {type: "agmux:data-request", reqId, name}
//	parent → iframe: {type: "agmux:data-response", reqId, ok, status, contentType, text, error?}
//
// When the document is opened directly (no parent) or the parent doesn't
// answer within 15s, it falls back to a direct fetch of the data URL.
// __AGMUX_ARTIFACT_ID__ is replaced with the (hex) artifact id.
const artifactDataHelperJS = `(function(){
if (window.agmuxData) return;
var base = "/artifacts/__AGMUX_ARTIFACT_ID__/data/";
var pending = {}, seq = 0;
function wrap(ok, status, contentType, body){
  body = typeof body === "string" ? body : "";
  return { ok: !!ok, status: status, contentType: contentType || "",
    text: function(){ return Promise.resolve(body); },
    json: function(){ return Promise.resolve().then(function(){ return JSON.parse(body); }); } };
}
function direct(name){
  return fetch(base + encodeURIComponent(name)).then(function(r){
    return r.text().then(function(t){ return wrap(r.ok, r.status, r.headers.get("Content-Type"), t); });
  });
}
window.addEventListener("message", function(e){
  if (e.source !== window.parent) return;
  var d = e.data;
  if (!d || d.type !== "agmux:data-response" || typeof d.reqId !== "string") return;
  var p = pending[d.reqId];
  if (!p) return;
  delete pending[d.reqId];
  clearTimeout(p.timer);
  if (!d.ok && !d.status) { p.reject(new Error(d.error || "agmuxData: request failed")); return; }
  p.resolve(wrap(d.ok, d.status, d.contentType, d.text));
});
window.agmuxData = function(name){
  name = String(name);
  if (window.parent === window) return direct(name);
  return new Promise(function(resolve, reject){
    var reqId = "agmux-" + (++seq) + "-" + Math.random().toString(36).slice(2);
    var fallback = function(){ delete pending[reqId]; direct(name).then(resolve, reject); };
    var timer = setTimeout(fallback, 15000);
    pending[reqId] = { resolve: resolve, reject: reject, timer: timer };
    try {
      window.parent.postMessage({ type: "agmux:data-request", reqId: reqId, name: name }, "*");
    } catch (err) { clearTimeout(timer); fallback(); }
  });
};
})();`

var (
	headTagPattern = regexp.MustCompile(`(?i)<head(\s[^>]*)?>`)
	bodyTagPattern = regexp.MustCompile(`(?i)<body[\s>]`)
	doctypePattern = regexp.MustCompile(`(?i)^\s*<!doctype[^>]*>`)
	utf8BOM        = []byte{0xEF, 0xBB, 0xBF}
)

// artifactDataHelperTag returns the <script> element defining agmuxData for
// the artifact id.
func artifactDataHelperTag(id string) []byte {
	return []byte("<script>" + strings.ReplaceAll(artifactDataHelperJS, "__AGMUX_ARTIFACT_ID__", id) + "</script>")
}

// injectArtifactDataHelper inserts the agmuxData helper script right after
// the <head> tag. Without a <head> (before <body>) it goes after the BOM and
// doctype, so the document's mode is not changed.
func injectArtifactDataHelper(html []byte, id string) []byte {
	script := artifactDataHelperTag(id)
	searchEnd := len(html)
	if loc := bodyTagPattern.FindIndex(html); loc != nil {
		searchEnd = loc[0]
	}
	pos := 0
	if loc := headTagPattern.FindIndex(html[:searchEnd]); loc != nil {
		pos = loc[1]
	} else {
		if bytes.HasPrefix(html, utf8BOM) {
			pos = len(utf8BOM)
		}
		if loc := doctypePattern.FindIndex(html[pos:]); loc != nil {
			pos += loc[1]
		}
	}
	out := make([]byte, 0, len(html)+len(script))
	out = append(out, html[:pos]...)
	out = append(out, script...)
	return append(out, html[pos:]...)
}
