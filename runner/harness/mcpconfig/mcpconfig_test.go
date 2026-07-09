package mcpconfig

import (
	"strings"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestAddOpenCodeServersFillsBearerTokenFromRunnerEnv(t *testing.T) {
	t.Setenv("EXAMPLE_MCP_TOKEN", "runner-secret")
	servers := map[string]any{}
	err := AddOpenCodeServers(servers, []task.MCPProfile{{
		Name:           "context",
		Transport:      "http",
		URL:            "https://mcp.example.com/mcp",
		Headers:        map[string]string{"X-Tenant": "engineering"},
		BearerTokenEnv: "EXAMPLE_MCP_TOKEN",
	}})
	if err != nil {
		t.Fatalf("AddOpenCodeServers: %v", err)
	}
	server := servers["context"].(map[string]any)
	headers := server["headers"].(map[string]string)
	if headers["Authorization"] != "Bearer runner-secret" || headers["X-Tenant"] != "engineering" {
		t.Fatalf("unexpected headers: %#v", headers)
	}
}

func TestAddOpenCodeServersFailsWhenBearerTokenEnvIsMissing(t *testing.T) {
	t.Setenv("MISSING_MCP_TOKEN", "")
	err := AddOpenCodeServers(map[string]any{}, []task.MCPProfile{{
		Name:           "context",
		URL:            "https://mcp.example.com/mcp",
		BearerTokenEnv: "MISSING_MCP_TOKEN",
	}})
	if err == nil || !strings.Contains(err.Error(), "missing bearer token env MISSING_MCP_TOKEN") {
		t.Fatalf("expected missing token error, got %v", err)
	}
}

func TestAddOpenCodeServersRejectsResolvedAuthorizationHeader(t *testing.T) {
	err := AddOpenCodeServers(map[string]any{}, []task.MCPProfile{{
		Name:    "context",
		URL:     "https://mcp.example.com/mcp",
		Headers: map[string]string{"authorization": "Bearer leaked"},
	}})
	if err == nil || !strings.Contains(err.Error(), "bearer_token_env") {
		t.Fatalf("expected authorization header rejection, got %v", err)
	}
}

func TestAddOpenCodeServersRejectsUnsafeURL(t *testing.T) {
	err := AddOpenCodeServers(map[string]any{}, []task.MCPProfile{{
		Name: "context",
		URL:  "https://user:secret@mcp.example.com/mcp",
	}})
	if err == nil || !strings.Contains(err.Error(), "without credentials") {
		t.Fatalf("expected URL credential rejection, got %v", err)
	}
}

func TestAddOpenCodeServersRejectsUnsafeName(t *testing.T) {
	err := AddOpenCodeServers(map[string]any{}, []task.MCPProfile{{
		Name: "../context",
		URL:  "https://mcp.example.com/mcp",
	}})
	if err == nil || !strings.Contains(err.Error(), "must start with a letter or number") {
		t.Fatalf("expected unsafe name rejection, got %v", err)
	}
}
