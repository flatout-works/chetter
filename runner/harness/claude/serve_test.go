package claude

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flatout-works/chetter/runner/harness/transport"
)

func TestSummarizeClaudeToolUse(t *testing.T) {
	got := summarizeClaudeEvent(&transport.Event{
		Type: "tool_use",
		Data: `{"content_block":{"name":"Bash"}}`,
	})
	if got != "tool_use: Bash" {
		t.Fatalf("summary = %q, want %q", got, "tool_use: Bash")
	}
}

func TestExtractClaudeDeltaText(t *testing.T) {
	got := extractClaudeDeltaText(`{"delta":{"type":"text_delta","text":"Inspecting code"}}`)
	if got != "Inspecting code" {
		t.Fatalf("text = %q, want %q", got, "Inspecting code")
	}
}

func TestSummarizeClaudeAPIRetry(t *testing.T) {
	got := summarizeClaudeEvent(&transport.Event{
		Type: "api_retry",
		Data: `{"type":"system","subtype":"api_retry","attempt":2,"max_retries":10,"retry_delay_ms":1500,"error_status":429,"error":"rate_limit"}`,
	})
	want := "system.api_retry: rate_limit, HTTP 429, attempt 2/10, retry in 1.5s"
	if got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

func TestExtractClaudeTokenUsageIncludesCacheTokens(t *testing.T) {
	usage := extractClaudeTokenUsage(`{"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":100,"cache_creation_input_tokens":50},"total_cost_usd":0.42}`)
	if usage == nil {
		t.Fatal("usage = nil")
	}
	if usage.InputTokens != 10 || usage.OutputTokens != 5 {
		t.Fatalf("tokens = %+v", usage)
	}
	if usage.CacheReadTokens != 100 || usage.CacheWriteTokens != 50 {
		t.Fatalf("cache tokens = %+v", usage)
	}
	if usage.CostCents != 42 {
		t.Fatalf("cost cents = %d", usage.CostCents)
	}
}

func TestGetSessionStatusAndContinue(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/session/proxy-id/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"status":"busy"}`)
	})
	continueCalls := make(chan string, 1)
	mux.HandleFunc("/session/proxy-id/continue", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		continueCalls <- string(body)
		fmt.Fprint(w, `{}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	status, err := getSessionStatus(context.Background(), server.URL, "proxy-id", "")
	if err != nil || status != "busy" {
		t.Fatalf("getSessionStatus = %q, %v", status, err)
	}

	if err := continueSession(context.Background(), server.URL, "proxy-id", ""); err != nil {
		t.Fatalf("continueSession: %v", err)
	}
	select {
	case body := <-continueCalls:
		if !strings.Contains(body, "Continue working on the current task") {
			t.Fatalf("continue body = %q", body)
		}
	case <-time.After(time.Second):
		t.Fatal("continue was not called")
	}
}

func TestContinueSessionToleratesBusyConflict(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer server.Close()

	if err := continueSession(context.Background(), server.URL, "proxy-id", ""); err != nil {
		t.Fatalf("continueSession on busy session must be a no-op, got %v", err)
	}
}
