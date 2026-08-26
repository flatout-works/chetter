// Package webui serves the built Chetter web UI.
package webui

import (
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"io/fs"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

// inlineScriptRe matches every <script>...</script> block; scripts with a src
// attribute are filtered out by the caller.
var inlineScriptRe = regexp.MustCompile(`(?s)<script\b([^>]*)>(.*?)</script>`)

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

// CSPScriptHashes returns the base64 SHA-256 hashes of every inline <script>
// block in the served index.html. The SPA's hydration bootstrap and theme
// scripts are inline (SvelteKit emits them), so a strict script-src CSP must
// carry their hashes. Computing the hashes from the actually-served file at
// startup keeps the CSP valid across rebuilds without weakening it with
// 'unsafe-inline'. Returns nil when no UI is present.
func CSPScriptHashes() []string {
	if dist, ok := embeddedDist(); ok {
		return inlineScriptHashes(dist)
	}
	if dist, ok := localDist(); ok {
		return inlineScriptHashes(dist)
	}
	return nil
}

// inlineScriptHashes extracts inline script bodies from index.html and hashes
// the exact text content the browser would hash (the raw bytes between the
// script tags).
func inlineScriptHashes(dist fs.FS) []string {
	data, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		return nil
	}
	var hashes []string
	for _, match := range inlineScriptRe.FindAllStringSubmatch(string(data), -1) {
		attrs, body := match[1], match[2]
		if strings.Contains(attrs, "src=") || strings.TrimSpace(body) == "" {
			continue
		}
		sum := sha256.Sum256([]byte(body))
		hashes = append(hashes, base64.StdEncoding.EncodeToString(sum[:]))
	}
	return hashes
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
