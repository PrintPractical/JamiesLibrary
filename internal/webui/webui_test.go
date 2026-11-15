package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var built = fstest.MapFS{
	"index.html":       {Data: []byte("<html>app</html>")},
	"assets/app-1.js":  {Data: []byte("console.log(1)")},
	"assets/app-1.css": {Data: []byte("a{}")},
	"assets/z.wasm":    {Data: []byte("\x00asm")},
	"favicon.svg":      {Data: []byte("<svg/>")},
	".gitkeep":         {Data: nil},
}

func get(h http.Handler, method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func TestContentTypes(t *testing.T) {
	h := NewHandler(built)
	cases := map[string]string{
		"/":                 "text/html",
		"/assets/app-1.js":  "text/javascript",
		"/assets/app-1.css": "text/css",
		"/assets/z.wasm":    "application/wasm",
		"/favicon.svg":      "image/svg+xml",
	}
	for p, want := range cases {
		rec := get(h, "GET", p)
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), want) {
			t.Errorf("%s: %d %q, want %s", p, rec.Code, rec.Header().Get("Content-Type"), want)
		}
	}
	if got := get(h, "GET", "/assets/app-1.js").Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("hashed asset cache-control %q", got)
	}
	if got := get(h, "GET", "/").Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("index cache-control %q", got)
	}
}

func TestUnknownRoutesFallBackToIndex(t *testing.T) {
	h := NewHandler(built)
	for _, p := range []string{"/books/12", "/desk/checkout", "/signin", "/index.html", "/a/../b"} {
		rec := get(h, "GET", p)
		if rec.Code != 200 || rec.Body.String() != "<html>app</html>" {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Body.String())
		}
	}
}

func TestMissingFileWithExtensionIs404(t *testing.T) {
	rec := get(NewHandler(built), "GET", "/assets/missing.js")
	if rec.Code != 404 || strings.Contains(rec.Body.String(), "<html>") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestDotfilesAreNotServed(t *testing.T) {
	rec := get(NewHandler(built), "GET", "/.gitkeep")
	if rec.Body.String() != "<html>app</html>" {
		t.Fatalf("dotfile served: %q", rec.Body.String())
	}
}

func TestOnlyGetAndHead(t *testing.T) {
	h := NewHandler(built)
	if rec := get(h, "POST", "/"); rec.Code != 405 {
		t.Errorf("POST: %d", rec.Code)
	}
	if rec := get(h, "HEAD", "/"); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d %q", rec.Code, rec.Body.String())
	}
}

func TestUnbuiltCheckoutStillServesAPage(t *testing.T) {
	rec := get(NewHandler(fstest.MapFS{".gitkeep": {}}), "GET", "/anything")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "without the web app") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestEmbeddedHandlerWorks(t *testing.T) {
	if rec := get(Handler(), "GET", "/"); rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}
