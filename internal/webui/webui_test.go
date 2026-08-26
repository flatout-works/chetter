package webui

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func TestNewHandlerServesStaticFile(t *testing.T) {
	handler := NewHandler(testDist())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/asset.js", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "console.log('ok');" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestNewHandlerFallsBackToIndex(t *testing.T) {
	handler := NewHandler(testDist())
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/tasks/task_123", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "<html>app</html>" {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func testDist() fs.FS {
	return fstest.MapFS{
		"index.html": {Data: []byte("<html>app</html>")},
		"asset.js":   {Data: []byte("console.log('ok');")},
	}
}

func TestInlineScriptHashes(t *testing.T) {
	inline := "console.log('inline');"
	other := "window.__boot = 1;"
	index := "<html><head>" +
		"<script>" + inline + "</script>" +
		"<script src=\"/_app/start.js\"></script>" +
		"</head><body>" +
		"<script>\n  " + other + "\n</script>" +
		"<script></script>" +
		"</body></html>"
	dist := fstest.MapFS{"index.html": {Data: []byte(index)}}

	hashes := inlineScriptHashes(dist)
	if len(hashes) != 2 {
		t.Fatalf("hashes = %v, want 2 (external and empty scripts skipped)", hashes)
	}

	// The hashes must match the exact raw text between the script tags.
	// The second block includes the surrounding whitespace/newlines.
	want1 := sha256Base64(inline)
	want2 := sha256Base64("\n  " + other + "\n")
	if hashes[0] != want1 {
		t.Errorf("hash[0] = %q, want %q", hashes[0], want1)
	}
	if hashes[1] != want2 {
		t.Errorf("hash[1] = %q, want %q", hashes[1], want2)
	}
}

func TestInlineScriptHashesWithoutIndex(t *testing.T) {
	if got := inlineScriptHashes(fstest.MapFS{}); got != nil {
		t.Errorf("hashes without index.html = %v, want nil", got)
	}
}

// TestInlineScriptHashesAgainstLocalBuild guards the strict script-src CSP
// against build-output drift: every inline <script> in the real built
// index.html must produce a hash, and scripts with a src attribute must not.
// Skipped when the web UI has not been built.
func TestInlineScriptHashesAgainstLocalBuild(t *testing.T) {
	dist := os.DirFS("../../web/build")
	if _, err := fs.Stat(dist, "index.html"); err != nil {
		t.Skip("web/build not present; run npm run build in web/ first")
	}
	data, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	hashes := inlineScriptHashes(dist)

	var inline, external int
	for _, match := range inlineScriptRe.FindAllStringSubmatch(string(data), -1) {
		if strings.Contains(match[1], "src=") {
			external++
			continue
		}
		if strings.TrimSpace(match[2]) != "" {
			inline++
		}
	}
	if len(hashes) != inline {
		t.Fatalf("hashes = %d, want %d (inline scripts in build output)", len(hashes), inline)
	}
	if inline == 0 {
		t.Fatal("built index.html has no inline scripts; CSP hash path is untested against reality")
	}
	for _, h := range hashes {
		if len(h) != 44 { // base64 of 32 bytes
			t.Errorf("hash %q is not a base64 SHA-256", h)
		}
	}
}

func TestCSPScriptHashesResolvesDist(t *testing.T) {
	// The embedded dist (or local web/build) is resolved by the same logic as
	// Handler(); the result is either nil (no UI built) or non-empty hashes.
	// The critical property is that it does not panic and is deterministic.
	first := CSPScriptHashes()
	second := CSPScriptHashes()
	if len(first) != len(second) {
		t.Fatalf("CSPScriptHashes not deterministic: %v vs %v", first, second)
	}
}

func sha256Base64(text string) string {
	sum := sha256.Sum256([]byte(text))
	return base64.StdEncoding.EncodeToString(sum[:])
}
