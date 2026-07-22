package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWaitForReadyRetriesUntilSuccess(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		if attempts < 2 {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := WaitForReady(context.Background(), server.URL, "/health", func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer secret")
	}, 2*time.Second, "test server")
	if err != nil {
		t.Fatalf("WaitForReady: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestWaitForReadyHonorsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "starting", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := WaitForReady(ctx, server.URL, "/health", nil, time.Second, "test server")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestWaitForReadyReportsLastStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "starting", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	err := WaitForReady(context.Background(), server.URL, "/health", nil, 10*time.Millisecond, "test server")
	if err == nil || !strings.Contains(err.Error(), "last status: 503") {
		t.Fatalf("error = %v, want last status", err)
	}
}

func TestEventReaderReadsMultilineData(t *testing.T) {
	reader := NewEventReader(strings.NewReader("event: message\ndata: first\ndata: second\n\nevent: done\ndata: final"))

	first, err := reader.Read()
	if err != nil {
		t.Fatalf("first Read: %v", err)
	}
	if first.Type != "message" || first.Data != "first\nsecond" {
		t.Fatalf("first event = %#v", first)
	}

	second, err := reader.Read()
	if err != nil {
		t.Fatalf("second Read: %v", err)
	}
	if second.Type != "done" || second.Data != "final" {
		t.Fatalf("second event = %#v", second)
	}
}
