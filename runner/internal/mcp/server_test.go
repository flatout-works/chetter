package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func makeServer(t *testing.T) (*Server, func()) {
	t.Helper()
	srv, err := NewServer()
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv, func() { srv.Close() }
}

func mcpCall(t *testing.T, addr, method string, params map[string]any) map[string]any {
	t.Helper()
	_, port, _ := net.SplitHostPort(addr)
	url := "http://127.0.0.1:" + port + "/mcp"

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})

	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	sessionID := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode == 202 && sessionID != "" {
		// Streamable HTTP: poll response via GET
		for range 10 {
			time.Sleep(100 * time.Millisecond)
			req, _ := http.NewRequest("GET", url, nil)
			req.Header.Set("Mcp-Session-Id", sessionID)
			req.Header.Set("Accept", "application/json")
			client := &http.Client{Timeout: 2 * time.Second}
			r, err := client.Do(req)
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(r.Body)
			r.Body.Close()
			if r.StatusCode == 200 {
				respBody = data
				break
			}
		}
	}

	var result map[string]any
	json.Unmarshal(respBody, &result)
	return result
}

func TestServerToolsList(t *testing.T) {
	srv, cleanup := makeServer(t)
	defer cleanup()

	result := mcpCall(t, srv.Addr(), "tools/list", map[string]any{})
	if err, ok := result["error"]; ok {
		t.Fatalf("tools/list error: %v", err)
	}
}

func TestServerToolsListAfterRegistration(t *testing.T) {
	srv, cleanup := makeServer(t)
	defer cleanup()

	srv.RegisterTool(ToolDef{
		Name:        "echo",
		Description: "Echo back a message",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"msg": map[string]string{"type": "string"},
			},
			"required": []string{"msg"},
		},
	}, func(ctx context.Context, args map[string]any) (any, error) {
		return args["msg"], nil
	})

	result := mcpCall(t, srv.Addr(), "tools/list", map[string]any{})
	if err, ok := result["error"]; ok {
		t.Fatalf("tools/list error: %v", err)
	}
	resultMap, _ := result["result"].(map[string]any)
	tools, _ := resultMap["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
}

func TestServerMultipleTools(t *testing.T) {
	srv, cleanup := makeServer(t)
	defer cleanup()

	for _, td := range ToolDefinitions() {
		def := td
		srv.RegisterTool(def, func(ctx context.Context, args map[string]any) (any, error) {
			return fmt.Sprintf("%s called", def.Name), nil
		})
	}

	result := mcpCall(t, srv.Addr(), "tools/list", map[string]any{})
	resultMap, _ := result["result"].(map[string]any)
	tools, _ := resultMap["tools"].([]any)
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}
}
