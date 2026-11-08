// Package web embeds the built ContentRemixer SPA (the Vite/React static build
// under dist/) and serves it from the Booth binary — one deployable artifact, no
// Node runtime in the cluster (§2). The build output is committed so the Go build
// needs no JS toolchain; web-ci regenerates it and fails on drift.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Assets is the embedded SPA build rooted at the dist directory.
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// Unreachable: dist is embedded at compile time, so Sub always resolves.
		panic("web: embedded dist missing: " + err.Error())
	}
	return sub
}

// Handler serves the SPA: hashed static assets by path, and index.html as the
// fallback for any unknown path so client-side routes (/content, /admin, …) deep
// link correctly. API routes (/v1/*) and health probes are mounted ahead of this
// on the Booth mux, so they never reach the fallback.
func Handler() http.Handler {
	assets := Assets()
	fileServer := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if reqPath == "" {
			reqPath = "index.html"
		}

		// Serve a real embedded file when it exists; otherwise fall back to the
		// SPA entrypoint (deep-link / client route).
		if f, err := assets.Open(reqPath); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}

		serveIndex(w, r, assets)
	})
}

func serveIndex(w http.ResponseWriter, _ *http.Request, assets fs.FS) {
	data, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		http.Error(w, "web assets unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// index.html must never be cached — it references the current hashed bundles.
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}
