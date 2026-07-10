package mcpconfig

import (
	"strings"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestHarnessServerFormatsReferenceTokenEnvironment(t *testing.T) {
	profile := task.MCPProfile{
		Name:           "context",
		Transport:      "sse",
		URL:            "https://mcp.example.com/sse",
		Headers:        map[string]string{"X-Tenant": "engineering"},
		BearerTokenEnv: "EXAMPLE_MCP_TOKEN",
	}

	servers := map[string]any{}
	if err := AddOpenCodeServers(servers, []task.MCPProfile{profile}); err != nil {
		t.Fatal(err)
	}
	if got := servers["context"].(map[string]any)["headers"].(map[string]string)["Authorization"]; got != "Bearer {env:EXAMPLE_MCP_TOKEN}" {
		t.Fatalf("unexpected OpenCode authorization header: %q", got)
	}

	servers = map[string]any{}
	if err := AddClaudeServers(servers, []task.MCPProfile{profile}); err != nil {
		t.Fatal(err)
	}
	if got := servers["context"].(map[string]any)["headers"].(map[string]string)["Authorization"]; got != "Bearer ${EXAMPLE_MCP_TOKEN}" {
		t.Fatalf("unexpected Claude authorization header: %q", got)
	}

	servers = map[string]any{}
	if err := AddCodeWhaleServers(servers, []task.MCPProfile{profile}); err != nil {
		t.Fatal(err)
	}
	if server := servers["context"].(map[string]any); server["transport"] != "sse" || server["headers"].(map[string]string)["Authorization"] != "Bearer ${EXAMPLE_MCP_TOKEN}" {
		t.Fatalf("unexpected CodeWhale server: %#v", server)
	}

	servers = map[string]any{}
	if err := AddPiServers(servers, []task.MCPProfile{profile}); err != nil {
		t.Fatal(err)
	}
	if server := servers["context"].(map[string]any); server["auth"] != "bearer" || server["bearerTokenEnv"] != "EXAMPLE_MCP_TOKEN" {
		t.Fatalf("unexpected Pi server: %#v", server)
	}
}

func TestHarnessServersRejectLiteralAuthorization(t *testing.T) {
	err := AddOpenCodeServers(map[string]any{}, []task.MCPProfile{{
		Name:    "context",
		URL:     "https://mcp.example.com/mcp",
		Headers: map[string]string{"authorization": "Bearer leaked"},
	}})
	if err == nil || !strings.Contains(err.Error(), "bearer_token_env") {
		t.Fatalf("expected literal authorization rejection, got %v", err)
	}
}
