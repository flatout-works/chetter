package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthMiddleware(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	t.Run("empty token passes through", func(t *testing.T) {
		nextCalled = false
		handler := authMiddleware("", nil, next)
		req := httptest.NewRequest("POST", "/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if !nextCalled {
			t.Error("expected next handler to be called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("correct bearer token", func(t *testing.T) {
		nextCalled = false
		handler := authMiddleware("mytoken", nil, next)
		req := httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Authorization", "Bearer mytoken")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if !nextCalled {
			t.Error("expected next handler to be called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("wrong bearer token", func(t *testing.T) {
		nextCalled = false
		handler := authMiddleware("mytoken", nil, next)
		req := httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if nextCalled {
			t.Error("expected next handler NOT to be called")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("missing authorization header", func(t *testing.T) {
		nextCalled = false
		handler := authMiddleware("mytoken", nil, next)
		req := httptest.NewRequest("POST", "/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if nextCalled {
			t.Error("expected next handler NOT to be called")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("non-bearer scheme", func(t *testing.T) {
		nextCalled = false
		handler := authMiddleware("mytoken", nil, next)
		req := httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if nextCalled {
			t.Error("expected next handler NOT to be called")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})
}
