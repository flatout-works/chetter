package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTokenUsageDelta(t *testing.T) {
	s := &session{}
	first, ok := tokenUsageDelta(s, json.RawMessage(`{"tokenUsage":{"total":{"inputTokens":10,"cachedInputTokens":4,"outputTokens":3,"reasoningOutputTokens":2}}}`))
	if !ok || first.InputTokens != 10 || first.CacheReadTokens != 4 || first.OutputTokens != 3 || first.ReasoningTokens != 2 {
		t.Fatalf("first usage = %+v, ok=%v", first, ok)
	}
	second, ok := tokenUsageDelta(s, json.RawMessage(`{"tokenUsage":{"total":{"inputTokens":15,"cachedInputTokens":5,"outputTokens":8,"reasoningOutputTokens":3}}}`))
	if !ok || second.InputTokens != 5 || second.CacheReadTokens != 1 || second.OutputTokens != 5 || second.ReasoningTokens != 1 {
		t.Fatalf("second usage = %+v, ok=%v", second, ok)
	}
}

func TestItemDetail(t *testing.T) {
	if got := itemDetail(json.RawMessage(`{"item":{"type":"commandExecution","command":"go test ./..."}}`)); got != "command: go test ./..." {
		t.Fatalf("command detail = %q", got)
	}
	if got := itemDetail(json.RawMessage(`{"item":{"type":"mcpToolCall","tool":"create_pr"}}`)); got != "MCP tool: create_pr" {
		t.Fatalf("MCP detail = %q", got)
	}
}

func TestSessionExportPathUsesCodexHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "/tmp/codex")
	if got := sessionExportPath("thread_1"); got != "/tmp/codex/session-thread_1.md" {
		t.Fatalf("path = %q", got)
	}
}

func TestBasicAuthHeader(t *testing.T) {
	if got := basicAuthHeader("secret"); !strings.HasPrefix(got, "Basic ") {
		t.Fatalf("header = %q", got)
	}
}
