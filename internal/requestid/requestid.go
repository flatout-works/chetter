// Package requestid provides HTTP request correlation IDs (issue #87).
//
// Every handled request carries a bounded, sanitized ID: a valid inbound
// X-Request-ID header is accepted, otherwise a fresh ID is generated at HTTP
// ingress. The ID is returned in the response, stored in the request
// context, and logged so downstream ingress/error logs can correlate.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Header is the HTTP header used to accept and return the request ID.
const Header = "X-Request-ID"

// MaxLength bounds a request ID after sanitization. Longer inbound values
// are rejected and replaced with a generated ID.
const MaxLength = 64

type ctxKey struct{}

// New returns a fresh random request ID of the form req_<32 hex chars>.
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "req_" + hex.EncodeToString(b[:])
	}
	// crypto/rand failures are effectively impossible on supported
	// platforms; fall back to a bounded time-based ID so request handling
	// and logging never break.
	return "req_" + strconv.FormatInt(time.Now().UnixNano(), 16) + "00000000000000000000"
}

// Sanitize validates and bounds an inbound request ID. Only 1..MaxLength
// alphanumeric, dash, and underscore characters are accepted; anything else
// (whitespace, control characters, oversized values) yields "" so callers
// generate a fresh ID. This keeps logged IDs free of log-injection payloads.
func Sanitize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > MaxLength {
		return ""
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			return ""
		}
	}
	return s
}

// FromContext returns the request ID stored in ctx, or "" when absent.
func FromContext(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// WithContext returns a context carrying id.
func WithContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// Middleware returns an http.Handler that assigns a request ID at ingress:
// a valid X-Request-ID header is accepted (bounded and sanitized), otherwise
// a fresh ID is generated. The ID is returned in the response, stored in the
// request context, and logged so downstream handlers can correlate their
// ingress/error logs. Infrastructure probe endpoints (/healthz, /readyz,
// /metrics) are logged at debug level to avoid probe noise (issue #87).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := Sanitize(r.Header.Get(Header))
		if id == "" {
			id = New()
		}
		w.Header().Set(Header, id)
		ctx := WithContext(r.Context(), id)
		level := slog.LevelInfo
		switch r.URL.Path {
		case "/healthz", "/readyz", "/metrics":
			level = slog.LevelDebug
		}
		slog.Log(ctx, level, "http request",
			"request_id", id,
			"method", r.Method,
			"path", r.URL.Path,
			"remote_addr", r.RemoteAddr,
		)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
