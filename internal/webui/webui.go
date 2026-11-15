// Package webui serves the built web app embedded in the binary.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// dist holds the output of `npm run build` in web/. An unbuilt checkout has
// only the .gitkeep that keeps the directory, so `go build` works without it.
//
//go:embed all:dist
var embedded embed.FS

// Handler serves the app embedded in the binary.
func Handler() http.Handler {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err) // dist is embedded at compile time
	}
	return NewHandler(sub)
}

const unbuilt = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Jamie's Library</title></head>
<body><h1>Jamie's Library</h1>
<p>This binary was built without the web app. Run <code>npm run build</code> in <code>web/</code> and build again.</p>
</body></html>
`

// NewHandler serves files from fsys. A path that names no file, and has no
// file extension, is a client-side route and gets index.html; a missing file
// with an extension is a 404 so a script never arrives as HTML. If fsys has
// no index.html a short "not built" page stands in for it.
func NewHandler(fsys fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && name != "index.html" && !strings.HasPrefix(path.Base(name), ".") {
			if data, err := fs.ReadFile(fsys, name); err == nil {
				send(w, r, name, data)
				return
			}
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
		}
		data, err := fs.ReadFile(fsys, "index.html")
		if err != nil {
			data = []byte(unbuilt)
		}
		send(w, r, "index.html", data)
	})
}

func send(w http.ResponseWriter, r *http.Request, name string, data []byte) {
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if strings.HasPrefix(name, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // content-hashed names
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
}
