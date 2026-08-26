package webapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimitRejectsOverBudget(t *testing.T) {
	handler := RateLimit(2, time.Minute, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for i, want := range []int{http.StatusNoContent, http.StatusNoContent, http.StatusTooManyRequests} {
		req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
		req.RemoteAddr = "192.0.2.10:1234"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("request %d status = %d, want %d", i+1, rec.Code, want)
		}
		if want == http.StatusTooManyRequests && rec.Header().Get("Retry-After") != "60" {
			t.Errorf("Retry-After = %q, want 60", rec.Header().Get("Retry-After"))
		}
	}
}

func TestRateLimitIgnoresUntrustedForwardedAddress(t *testing.T) {
	handler := RateLimit(1, time.Minute, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for i, forwarded := range []string{"198.51.100.1", "198.51.100.2"} {
		req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
		req.RemoteAddr = "192.0.2.10:1234"
		req.Header.Set("X-Forwarded-For", forwarded)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		want := http.StatusNoContent
		if i == 1 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("request %d status = %d, want %d", i+1, rec.Code, want)
		}
	}
}

func TestRateLimitSeparatesDirectPeers(t *testing.T) {
	handler := RateLimit(1, time.Minute, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	for _, peer := range []string{"192.0.2.10:1234", "192.0.2.11:1234"} {
		req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
		req.RemoteAddr = peer
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("peer %s status = %d, want %d", peer, rec.Code, http.StatusNoContent)
		}
	}
}
