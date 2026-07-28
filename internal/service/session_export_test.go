package service

import (
	"strings"
	"testing"
)

func TestCompactSessionExportLeavesOtherHarnessesUnchanged(t *testing.T) {
	export := "# OpenCode Session\n\n## User\nHello"
	if got := compactSessionExport(export); got != export {
		t.Fatalf("compactSessionExport changed non-Pi export: %q", got)
	}
}

func TestCompactPiSessionExportRendersToolsWithoutAssistantMetadata(t *testing.T) {
	export := `# Pi Session Export

## 1. user

Inspect the code.

## 2. assistant

` + "```json" + `
{
  "role": "assistant",
  "provider": "provider-secret",
  "responseId": "response-secret",
  "usage": {"input": 1000},
  "content": [
    {"type": "thinking", "thinking": "private chain of thought"},
    {"type": "text", "text": "I will inspect the file."},
    {"type": "toolCall", "name": "read", "arguments": {"path": "internal/service/api.go", "offset": 1, "limit": 40}}
  ]
}
` + "```" + `

## 3. toolResult

package service
`

	got := compactSessionExport(export)
	for _, want := range []string{"# Pi Session Activity", "Inspect the code.", "I will inspect the file.", "### Tool: `read`", "`internal/service/api.go`", "## 3. Result: read", "package service"} {
		if !strings.Contains(got, want) {
			t.Errorf("compact export missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"private chain of thought", "provider-secret", "response-secret", `"usage"`} {
		if strings.Contains(got, unwanted) {
			t.Errorf("compact export contains filtered value %q:\n%s", unwanted, got)
		}
	}
}

func TestCompactPiSessionExportTruncatesLargeToolResults(t *testing.T) {
	result := strings.Repeat("large tool output\n", 1000)
	export := "# Pi Session Export\n\n## 1. toolResult\n\n" + result
	got := compactSessionExport(export)
	if !strings.Contains(got, "Result truncated in compact view") {
		t.Fatalf("expected truncation marker, got %d bytes", len(got))
	}
	if len(got) > compactToolResultMaxBytes+1000 {
		t.Fatalf("compact export too large: %d bytes", len(got))
	}
}
