// Package webui serves the built Chetter web UI.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// Handler returns an SPA file server for the embedded UI. During local
// development it falls back to web/build when embedded assets are absent.
func Handler() http.Handler {
	if dist, ok := embeddedDist(); ok {
		return NewHandler(dist)
	}
	if dist, ok := localDist(); ok {
		return NewHandler(dist)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "web UI has not been built", http.StatusNotFound)
	})
}

// NewHandler returns an HTTP handler that serves files from dist and falls back
// to index.html for client-side routes.
func NewHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if requestPath != "" && fileExists(dist, requestPath) {
			fileServer.ServeHTTP(w, r)
			return
		}

		indexReq := r.Clone(r.Context())
		indexReq.URL.Path = "/"
		fileServer.ServeHTTP(w, indexReq)
	})
}

func embeddedDist() (fs.FS, bool) {
	dist, err := fs.Sub(embedded, "dist")
	if err != nil || !fileExists(dist, "index.html") {
		return nil, false
	}
	return dist, true
}

func localDist() (fs.FS, bool) {
	dist := os.DirFS("web/build")
	if !fileExists(dist, "index.html") {
		return nil, false
	}
	return dist, true
}

func fileExists(dist fs.FS, name string) bool {
	info, err := fs.Stat(dist, name)
	return err == nil && !info.IsDir()
}
