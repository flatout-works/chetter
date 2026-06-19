package mcp

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func TestServerInitialize(t *testing.T) {
	s := &Server{
		tools: make(map[string]ToolHandler),
	}

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
		Params:  map[string]any{},
	}

	resp := s.handleRequest(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("initialize error: %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not map: %T", resp.Result)
	}
	serverInfo, ok := result["serverInfo"].(map[string]string)
	if !ok {
		t.Fatalf("serverInfo is not map[string]string: %T", result["serverInfo"])
	}
	if serverInfo["name"] != "chetter-runner" {
		t.Errorf("name = %q", serverInfo["name"])
	}
	if serverInfo["version"] != "0.1.0" {
		t.Errorf("version = %q", serverInfo["version"])
	}
}

func TestServerToolsList(t *testing.T) {
	s := &Server{
		tools: make(map[string]ToolHandler),
	}

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
		Params:  map[string]any{},
	}

	resp := s.handleRequest(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("tools/list error: %v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not map: %T", resp.Result)
	}
	tools, ok := result["tools"].([]map[string]any)
	if !ok {
		t.Fatalf("tools is not []map[string]any: %T", result["tools"])
	}
	if len(tools) < 3 {
		t.Errorf("too few tools: %d", len(tools))
	}
}

func TestServerToolsCallValid(t *testing.T) {
	s := &Server{
		tools: make(map[string]ToolHandler),
	}
	s.RegisterTool("echo", func(ctx context.Context, args map[string]any) (any, error) {
		return args["message"], nil
	})

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params: map[string]any{
			"name": "echo",
			"arguments": map[string]any{
				"message": "hello",
			},
		},
	}

	resp := s.handleRequest(context.Background(), req)
	if resp.Error != nil {
		t.Fatalf("tools/call error: %v", resp.Error)
	}
}

func TestServerToolsCallError(t *testing.T) {
	s := &Server{
		tools: make(map[string]ToolHandler),
	}
	s.RegisterTool("failing", func(ctx context.Context, args map[string]any) (any, error) {
		return nil, net.ErrClosed
	})

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "failing",
			"arguments": map[string]any{},
		},
	}

	resp := s.handleRequest(context.Background(), req)
	// Tool errors are returned as result with isError=true, not as JSON-RPC errors.
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result is not map: %T", resp.Result)
	}
	content, ok := result["content"].([]map[string]any)
	if !ok {
		t.Fatalf("content is not []map[string]any: %T", result["content"])
	}
	if len(content) == 0 || content[0]["text"] == nil {
		t.Fatal("expected error text in content")
	}
}

func TestServerToolsCallUnknown(t *testing.T) {
	s := &Server{
		tools: make(map[string]ToolHandler),
	}

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      5,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      "nonexistent",
			"arguments": map[string]any{},
		},
	}

	resp := s.handleRequest(context.Background(), req)
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code = %d, want -32601", resp.Error.Code)
	}
}

func TestServerToolsCallMissingName(t *testing.T) {
	s := &Server{
		tools: make(map[string]ToolHandler),
	}

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      6,
		Method:  "tools/call",
		Params: map[string]any{
			"arguments": map[string]any{},
		},
	}

	resp := s.handleRequest(context.Background(), req)
	if resp.Error == nil {
		t.Fatal("expected error for missing name")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("error code = %d, want -32602", resp.Error.Code)
	}
}

func TestServerInvalidJSONRPC(t *testing.T) {
	s := &Server{
		tools: make(map[string]ToolHandler),
	}

	req := &JSONRPCRequest{
		JSONRPC: "1.0",
		ID:      7,
		Method:  "initialize",
	}

	resp := s.handleRequest(context.Background(), req)
	if resp.Error == nil {
		t.Fatal("expected error for wrong JSON-RPC version")
	}
	if resp.Error.Code != -32600 {
		t.Errorf("error code = %d, want -32600", resp.Error.Code)
	}
}

func TestServerUnknownMethod(t *testing.T) {
	s := &Server{
		tools: make(map[string]ToolHandler),
	}

	req := &JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      8,
		Method:  "unknown/method",
	}

	resp := s.handleRequest(context.Background(), req)
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("error code = %d, want -32601", resp.Error.Code)
	}
}

func TestServerE2EViaUnixSocket(t *testing.T) {
	dir := t.TempDir()
	socketPath := dir + "/test.sock"

	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.Serve(ctx)
	defer srv.Close()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Send initialize request.
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	}
	reqBytes, _ := json.Marshal(req)
	if _, err := conn.Write(append(reqBytes, '\n')); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Read response (up to 4KB).
	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	respStr := strings.TrimSpace(string(buf[:n]))
	var resp map[string]any
	if err := json.Unmarshal([]byte(respStr), &resp); err != nil {
		t.Fatalf("unmarshal response %q: %v", respStr, err)
	}
	if resp["error"] != nil {
		t.Fatalf("response error: %v", resp["error"])
	}
}
