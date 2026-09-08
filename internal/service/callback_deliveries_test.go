package service

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestCallbackDeliveryBackoff(t *testing.T) {
	cases := []struct {
		attempts int32
		want     time.Duration
	}{
		{0, time.Second},
		{1, time.Second},
		{2, 5 * time.Second},
		{3, 15 * time.Second},
		{4, 30 * time.Second},
		{9, 30 * time.Second},
	}
	for _, tc := range cases {
		if got := callbackDeliveryBackoff(tc.attempts); got != tc.want {
			t.Errorf("callbackDeliveryBackoff(%d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}

func TestNextCallbackDeliveryAttempt(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	backoff := func(attempts int32) time.Duration {
		if attempts == 1 {
			return time.Second
		}
		return 5 * time.Second
	}

	t.Run("retry before max attempts", func(t *testing.T) {
		status, nextAt, retry := nextCallbackDeliveryAttempt(now, 1, 3, backoff)
		if !retry || status != callbackDeliveryStatusFailed {
			t.Fatalf("got (status=%q, retry=%v), want failed+retry", status, retry)
		}
		if want := now.Add(time.Second); !nextAt.Equal(want) {
			t.Errorf("next attempt = %v, want %v", nextAt, want)
		}
	})

	t.Run("dead letter when attempts reach max", func(t *testing.T) {
		status, _, retry := nextCallbackDeliveryAttempt(now, 3, 3, backoff)
		if retry || status != callbackDeliveryStatusDeadLetter {
			t.Fatalf("got (status=%q, retry=%v), want dead_letter, no retry", status, retry)
		}
	})

	t.Run("dead letter requires at least one attempt", func(t *testing.T) {
		status, _, retry := nextCallbackDeliveryAttempt(now, 1, 1, backoff)
		if retry || status != callbackDeliveryStatusDeadLetter {
			t.Fatalf("got (status=%q, retry=%v), want dead_letter, no retry", status, retry)
		}
	})

	t.Run("max attempts larger than default still retries", func(t *testing.T) {
		status, nextAt, retry := nextCallbackDeliveryAttempt(now, 4, 5, backoff)
		if !retry || status != callbackDeliveryStatusFailed {
			t.Fatalf("got (status=%q, retry=%v), want failed+retry", status, retry)
		}
		if want := now.Add(5 * time.Second); !nextAt.Equal(want) {
			t.Errorf("next attempt = %v, want %v", nextAt, want)
		}
	})
}

func TestBuildCallbackHTTPRequest(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults to POST JSON content type", func(t *testing.T) {
		req, err := buildCallbackHTTPRequest(ctx, "", "https://hooks.example.com/cb", nil, []byte(`{}`))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		if req.Method != http.MethodPost {
			t.Errorf("method = %q, want POST", req.Method)
		}
		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		body, _ := io.ReadAll(req.Body)
		if string(body) != "{}" {
			t.Errorf("body = %q, want {}", body)
		}
	})

	t.Run("explicit method, content type, and headers win", func(t *testing.T) {
		req, err := buildCallbackHTTPRequest(ctx, http.MethodPut, "https://hooks.example.com/cb",
			map[string]string{"Content-Type": "text/plain", "X-Hook": "v1"}, []byte("hi"))
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		if req.Method != http.MethodPut {
			t.Errorf("method = %q, want PUT", req.Method)
		}
		if got := req.Header.Get("Content-Type"); got != "text/plain" {
			t.Errorf("Content-Type = %q, want text/plain", got)
		}
		if got := req.Header.Get("X-Hook"); got != "v1" {
			t.Errorf("X-Hook = %q, want v1", got)
		}
	})
}
