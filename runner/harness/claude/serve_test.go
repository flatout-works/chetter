package claude

import (
	"testing"

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
