// Package webui embeds the built React web UI (web/, phase C) into the
// takos binary and serves it as a single-page app. The Vite build writes
// its output to internal/webui/dist (committed, so `go build` needs no
// node), which this package embeds.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler serves the embedded SPA: real files by their path, index.html as
// the fallback for client-side routes (e.g. /ws/alpha). ok is false when no
// UI was embedded — a dist with only the placeholder — so the daemon can
// skip mounting it.
func Handler() (h http.Handler, ok bool) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "assets"); err != nil {
		// Only the placeholder index.html — no real build present.
		return nil, false
	}
	return spa{fsys: sub, files: http.FileServer(http.FS(sub))}, true
}

type spa struct {
	fsys  fs.FS
	files http.Handler
}

func (s spa) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	if _, err := fs.Stat(s.fsys, p); err != nil {
		// Unknown path → let the SPA router handle it.
		http.ServeFileFS(w, r, s.fsys, "index.html")
		return
	}
	s.files.ServeHTTP(w, r)
}
