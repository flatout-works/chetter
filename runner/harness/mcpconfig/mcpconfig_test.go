package mcpconfig

import (
	"strings"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestResolveHeadersFailsForMissingEnv(t *testing.T) {
	profile := task.MCPProfile{
		Name: "test-profile",
		Headers: map[string]string{
			"Authorization": "Bearer ${env:DOES_NOT_EXIST_FOR_MCP_TEST}",
		},
	}
	_, err := ResolveHeaders(profile)
	if err == nil {
		t.Fatal("expected missing env error")
	}
	if !strings.Contains(err.Error(), "DOES_NOT_EXIST_FOR_MCP_TEST") {
		t.Fatalf("error %q does not mention missing env", err)
	}
}

func TestResolveHeadersFailsForEmptyEnv(t *testing.T) {
	t.Setenv("EMPTY_MCP_TOKEN", "")
	profile := task.MCPProfile{
		Name: "test-profile",
		Headers: map[string]string{
			"Authorization": "Bearer ${env:EMPTY_MCP_TOKEN}",
		},
	}
	_, err := ResolveHeaders(profile)
	if err == nil {
		t.Fatal("expected empty env error")
	}
	if !strings.Contains(err.Error(), "EMPTY_MCP_TOKEN") {
		t.Fatalf("error %q does not mention empty env", err)
	}
}

func TestAddHTTPServersRejectsReservedNamesCaseInsensitive(t *testing.T) {
	for _, name := range []string{"chetter", "CheTTeR", "runner-bridge", "RUnNeR-BrIdGe"} {
		t.Run(name, func(t *testing.T) {
			err := AddHTTPServers(map[string]any{}, []task.MCPProfile{{
				Name: name,
				URL:  "http://chetter-mcp:8080/mcp",
			}})
			if err == nil {
				t.Fatal("expected reserved name error")
			}
		})
	}
}

func TestAddHTTPServersRejectsToolAllowlist(t *testing.T) {
	err := AddHTTPServers(map[string]any{}, []task.MCPProfile{{
		Name:          "restricted",
		URL:           "https://mcp.example.test/mcp",
		ToolAllowlist: []string{"allowed_tool"},
	}})
	if err == nil {
		t.Fatal("expected tool_allowlist enforcement error")
	}
	if !strings.Contains(err.Error(), "cannot enforce per-tool MCP restrictions") {
		t.Fatalf("error = %q, want enforcement message", err)
	}
}

func TestAddPiServersRejectsToolAllowlist(t *testing.T) {
	err := AddPiServers(map[string]any{}, []task.MCPProfile{{
		Name:          "restricted",
		URL:           "https://mcp.example.test/mcp",
		ToolAllowlist: []string{"allowed_tool"},
	}})
	if err == nil {
		t.Fatal("expected tool_allowlist enforcement error")
	}
	if !strings.Contains(err.Error(), "cannot enforce per-tool MCP restrictions") {
		t.Fatalf("error = %q, want enforcement message", err)
	}
}
