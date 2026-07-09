package main

import (
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

func TestBuildClaudeCommandLoadsGeneratedMCPConfig(t *testing.T) {
	args, _ := buildClaudeCommand(messageRequest{Prompt: "review"})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--mcp-config /workspace/.mcp.json") || !strings.Contains(joined, "--strict-mcp-config") {
		t.Fatalf("Claude command does not load the generated MCP config: %v", args)
	}
}
