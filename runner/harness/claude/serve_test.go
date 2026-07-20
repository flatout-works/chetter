package claude

import "testing"

func TestSummarizeClaudeToolUse(t *testing.T) {
	got := summarizeClaudeEvent(&sseEvent{
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
