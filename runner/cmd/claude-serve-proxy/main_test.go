package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWithAuthAcceptsRunnerSecret(t *testing.T) {
	srv := &server{password: "secret"}
	handler := withAuth(srv, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.Header.Set("Authorization", basicAuthHeader("secret"))
	rr := httptest.NewRecorder()

	handler(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestProgressEventPayloadUsesNestedStreamEvent(t *testing.T) {
	payload, err := json.Marshal(progressEventPayload(map[string]any{
		"type": "stream_event",
		"event": map[string]any{
			"content_block": map[string]any{
				"name": "Bash",
			},
		},
	}))
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if string(payload) != `{"content_block":{"name":"Bash"}}` {
		t.Fatalf("payload = %s", payload)
	}
}

func TestWithAuthRejectsWrongSecret(t *testing.T) {
	srv := &server{password: "secret"}
	handler := withAuth(srv, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.Header.Set("Authorization", basicAuthHeader("wrong"))
	rr := httptest.NewRecorder()

	handler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestSessionRecordsTextDeltaAndResultError(t *testing.T) {
	s := &session{}
	s.recordStreamEvent(map[string]any{
		"type": "stream_event",
		"event": map[string]any{
			"delta": map[string]any{
				"type": "text_delta",
				"text": "hello",
			},
		},
	})
	s.recordStreamEvent(map[string]any{
		"type":  "result",
		"error": "boom",
	})

	if got := s.summary.String(); got != "hello" {
		t.Fatalf("summary = %q, want hello", got)
	}
	if !strings.Contains(s.runErr, "boom") {
		t.Fatalf("runErr = %q, want boom", s.runErr)
	}
}
