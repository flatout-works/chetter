package webui

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
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
