package webapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeaders(nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	tests := []struct {
		name     string
		target   string
		forward  string
		wantHSTS bool
	}{
		{name: "plain HTTP", target: "http://chetter.example.test/"},
		{name: "direct TLS", target: "https://chetter.example.test/", wantHSTS: true},
		{name: "spoofed forwarded proto", target: "http://chetter.example.test/", forward: "https"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			if tt.forward != "" {
				req.Header.Set("X-Forwarded-Proto", tt.forward)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
				t.Errorf("X-Frame-Options = %q, want DENY", got)
			}
			if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
			}
			if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "object-src 'none'") {
				t.Errorf("Content-Security-Policy = %q", csp)
			}
			if got := rec.Header().Get("Strict-Transport-Security") != ""; got != tt.wantHSTS {
				t.Errorf("HSTS present = %v, want %v", got, tt.wantHSTS)
			}
		})
	}
}

func TestBuildCSPInlineScriptHashes(t *testing.T) {
	hashes := []string{"abc123", "", "def456"}
	csp := BuildCSP(hashes)
	if !strings.Contains(csp, "script-src 'self' 'sha256-abc123' 'sha256-def456'") {
		t.Errorf("script-src hashes missing from CSP: %q", csp)
	}
	if !strings.Contains(csp, "style-src 'self' 'unsafe-inline'") {
		t.Errorf("CSP missing style-src inline allowance: %q", csp)
	}
	// The script-src directive specifically must never allow unsafe-inline.
	for _, directive := range strings.Split(csp, ";") {
		directive = strings.TrimSpace(directive)
		if strings.HasPrefix(directive, "script-src") && strings.Contains(directive, "unsafe-inline") {
			t.Errorf("script-src allows unsafe-inline: %q", directive)
		}
	}
}

func TestBuildCSPWithoutHashes(t *testing.T) {
	csp := BuildCSP(nil)
	if !strings.Contains(csp, "script-src 'self'") {
		t.Errorf("default script-src wrong: %q", csp)
	}
	if strings.Contains(csp, "sha256-") {
		t.Errorf("unexpected hash in CSP: %q", csp)
	}
}
