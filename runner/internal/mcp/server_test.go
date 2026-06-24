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

func mcpCall(t *testing.T, srv *Server, method string, params map[string]any) map[string]any {
	t.Helper()
	_, port, _ := net.SplitHostPort(srv.Addr())
	url := "http://127.0.0.1:" + port + "/mcp"

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})

	req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	sessionID := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode == 202 && sessionID != "" {
		for range 10 {
			time.Sleep(100 * time.Millisecond)
			req, _ := http.NewRequest("GET", url, nil)
			req.Header.Set("Mcp-Session-Id", sessionID)
			req.Header.Set("Accept", "application/json, text/event-stream")
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
	t.Logf("%s (status=%d): %s", method, resp.StatusCode, string(respBody))
	return result
}

func mcpInit(t *testing.T, srv *Server) {
	mcpCall(t, srv, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]string{"name": "test", "version": "1.0"},
	})
	time.Sleep(200 * time.Millisecond)
}

func TestServerToolsList(t *testing.T) {
	srv, cleanup := makeServer(t)
	defer cleanup()
	mcpInit(t, srv)

	result := mcpCall(t, srv, "tools/list", map[string]any{})
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

	mcpInit(t, srv)
	result := mcpCall(t, srv, "tools/list", map[string]any{})
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

	mcpInit(t, srv)
	result := mcpCall(t, srv, "tools/list", map[string]any{})
	resultMap, _ := result["result"].(map[string]any)
	tools, _ := resultMap["tools"].([]any)
	if len(tools) != 4 {
		t.Fatalf("expected 4 tools, got %d", len(tools))
	}
}
