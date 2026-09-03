package requestid

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var idPattern = regexp.MustCompile(`^req_[0-9a-f]{32}$`)

func TestNew(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := New()
		if !idPattern.MatchString(id) {
			t.Fatalf("generated ID %q does not match %v", id, idPattern)
		}
		if len(id) > MaxLength {
			t.Fatalf("generated ID %q exceeds MaxLength %d", id, MaxLength)
		}
		if seen[id] {
			t.Fatalf("generated duplicate ID %q", id)
		}
		seen[id] = true
	}
}

func TestSanitize(t *testing.T) {
	t.Run("accepts valid ids", func(t *testing.T) {
		for _, in := range []string{"abc123", "req_abc-123_XYZ", strings.Repeat("a", MaxLength)} {
			if got := Sanitize(in); got != in {
				t.Errorf("Sanitize(%q) = %q, want %q", in, got, in)
			}
		}
	})
	t.Run("trims surrounding whitespace", func(t *testing.T) {
		if got := Sanitize("  req_abc  "); got != "req_abc" {
			t.Errorf("expected trimmed id, got %q", got)
		}
	})
	t.Run("rejects empty and oversized", func(t *testing.T) {
		if got := Sanitize(""); got != "" {
			t.Errorf("expected empty for empty input, got %q", got)
		}
		if got := Sanitize(strings.Repeat("a", MaxLength+1)); got != "" {
			t.Errorf("expected empty for oversized input, got %q", got)
		}
	})
	t.Run("rejects log-injection characters", func(t *testing.T) {
		for _, in := range []string{
			"abc\ndef",         // newline injection
			"abc\tdef",         // tab
			"req_abc\x00def",   // NUL
			"req_abc;rm -rf /", // shell metacharacters
			"req_abc\"quoted",  // quotes
			"req_abc'quoted",   // single quotes
			"req_abc=1&x=2",    // query separators
			"req_abc def",      // internal whitespace
			"req_abc\u00e9",    // non-ASCII
		} {
			if got := Sanitize(in); got != "" {
				t.Errorf("Sanitize(%q) = %q, want empty", in, got)
			}
		}
	})
}

func TestFromContextWithContext(t *testing.T) {
	if got := FromContext(context.Background()); got != "" {
		t.Errorf("expected empty id from bare context, got %q", got)
	}
	ctx := WithContext(context.Background(), "req_abc")
	if got := FromContext(ctx); got != "req_abc" {
		t.Errorf("expected req_abc, got %q", got)
	}
}

func TestMiddleware(t *testing.T) {
	var logBuf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(prev)

	t.Run("generates an id when the header is missing", func(t *testing.T) {
		var gotID string
		h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotID = FromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		h.ServeHTTP(rec, req)

		if gotID == "" {
			t.Fatal("expected a generated request id in context")
		}
		if !idPattern.MatchString(gotID) {
			t.Fatalf("expected generated id shape, got %q", gotID)
		}
		if rec.Header().Get(Header) != gotID {
			t.Errorf("response header %s = %q, want %q", Header, rec.Header().Get(Header), gotID)
		}
	})

	t.Run("propagates a valid inbound id", func(t *testing.T) {
		var gotID string
		h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotID = FromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		req.Header.Set(Header, "my-trace-id_1")
		h.ServeHTTP(rec, req)

		if gotID != "my-trace-id_1" {
			t.Errorf("expected inbound id propagated, got %q", gotID)
		}
		if rec.Header().Get(Header) != "my-trace-id_1" {
			t.Errorf("response header %s = %q, want my-trace-id_1", Header, rec.Header().Get(Header))
		}
	})

	t.Run("rejects invalid inbound id and generates a fresh one", func(t *testing.T) {
		var gotID string
		h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotID = FromContext(r.Context())
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/webhook/github", nil)
		req.Header.Set(Header, "bad\nid")
		h.ServeHTTP(rec, req)

		if gotID == "" || !idPattern.MatchString(gotID) {
			t.Fatalf("expected fresh generated id for invalid inbound, got %q", gotID)
		}
		if rec.Header().Get(Header) != gotID {
			t.Errorf("response header %s = %q, want %q", Header, rec.Header().Get(Header), gotID)
		}
	})

	t.Run("logs ingress with request id", func(t *testing.T) {
		logBuf.Reset()
		h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		req.Header.Set(Header, "corr-123")
		h.ServeHTTP(rec, req)

		out := logBuf.String()
		if !strings.Contains(out, "msg=\"http request\"") {
			t.Errorf("expected ingress log line, got %q", out)
		}
		if !strings.Contains(out, "request_id=corr-123") {
			t.Errorf("expected request_id in ingress log, got %q", out)
		}
		if !strings.Contains(out, "path=/mcp") {
			t.Errorf("expected path in ingress log, got %q", out)
		}
	})

	t.Run("logs probe endpoints at debug level", func(t *testing.T) {
		logBuf.Reset()
		h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		h.ServeHTTP(rec, req)

		out := logBuf.String()
		if strings.Contains(out, "msg=\"http request\"") {
			t.Errorf("probe ingress should be filtered at info level, got %q", out)
		}
	})
}
