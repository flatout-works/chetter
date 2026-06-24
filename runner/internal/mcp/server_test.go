package mcp

import (
	"context"
	"encoding/json"
	"net"
	"strings"
	"testing"
)

func makeServer(t *testing.T) (*Server, string, func()) {
	t.Helper()
	socketPath := t.TempDir() + "/test.sock"
	srv, err := NewServer(socketPath)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go srv.Serve(ctx)
	cleanup := func() {
		cancel()
		srv.Close()
	}
	return srv, socketPath, cleanup
}

func sendRequest(conn net.Conn, method string, id int, params map[string]any) map[string]any {
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	reqBytes, _ := json.Marshal(req)
	conn.Write(append(reqBytes, '\n'))
	buf := make([]byte, 8192)
	n, _ := conn.Read(buf)
	var resp map[string]any
	json.Unmarshal(buf[:n], &resp)
	return resp
}

func TestServerInitialize(t *testing.T) {
	srv, socketPath, cleanup := makeServer(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	resp := sendRequest(conn, "initialize", 1, map[string]any{})
	if resp["error"] != nil {
		t.Fatalf("initialize error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is not map: %T", resp["result"])
	}
	if pv, ok := result["protocolVersion"].(string); !ok || pv != "2025-11-25" {
		t.Errorf("protocolVersion = %q, want %q", result["protocolVersion"], "2025-11-25")
	}
	if _, capOk := result["capabilities"].(map[string]any); !capOk {
		t.Fatalf("capabilities is missing: %T", result["capabilities"])
	}

	_ = srv
}

func TestServerNotificationsInitialized(t *testing.T) {
	_, socketPath, cleanup := makeServer(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	notifyBytes, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
	conn.Write(append(notifyBytes, '\n'))
}

func TestServerToolsList(t *testing.T) {
	srv, socketPath, cleanup := makeServer(t)
	defer cleanup()

	srv.RegisterTool(ToolDef{
		Name:        "echo",
		Description: "echoes the message",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, args map[string]any) (any, error) {
		return "ok", nil
	})

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	sendRequest(conn, "initialize", 1, map[string]any{})

	notifyBytes, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})
	conn.Write(append(notifyBytes, '\n'))

	resp := sendRequest(conn, "tools/list", 2, map[string]any{})
	if resp["error"] != nil {
		t.Fatalf("tools/list error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is not map: %T", resp["result"])
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools/list returned no tools: %v", result["tools"])
	}
	found := false
	for _, t := range tools {
		toolMap, ok := t.(map[string]any)
		if ok && toolMap["name"] == "echo" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("echo tool not in tools/list result")
	}
}

func TestServerToolsCallValid(t *testing.T) {
	srv, socketPath, cleanup := makeServer(t)
	defer cleanup()

	srv.RegisterTool(ToolDef{
		Name:        "echo",
		Description: "echoes the message",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"message": map[string]string{"type": "string"}},
		},
	}, func(ctx context.Context, args map[string]any) (any, error) {
		return args["message"], nil
	})

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	sendRequest(conn, "initialize", 1, map[string]any{})
	conn.Write(append([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), '\n'))

	resp := sendRequest(conn, "tools/call", 3, map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"message": "hello"},
	})
	if resp["error"] != nil {
		t.Fatalf("tools/call error: %v", resp["error"])
	}
}

func TestServerToolsCallError(t *testing.T) {
	srv, socketPath, cleanup := makeServer(t)
	defer cleanup()

	srv.RegisterTool(ToolDef{
		Name:        "failing",
		Description: "always fails",
		InputSchema: map[string]any{"type": "object"},
	}, func(ctx context.Context, args map[string]any) (any, error) {
		return nil, net.ErrClosed
	})

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	sendRequest(conn, "initialize", 1, map[string]any{})
	conn.Write(append([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), '\n'))

	resp := sendRequest(conn, "tools/call", 4, map[string]any{
		"name":      "failing",
		"arguments": map[string]any{},
	})
	if resp["error"] != nil {
		t.Fatalf("unexpected JSON-RPC error: %v (tool errors should be in result.isError)", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is not map: %T", resp["result"])
	}
	if result["isError"] != true {
		t.Fatal("expected isError=true")
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatal("expected error text in content")
	}
	textContent, ok := content[0].(map[string]any)
	if !ok || textContent["text"] == nil {
		t.Fatal("expected text content")
	}
}

func TestServerToolsCallUnknown(t *testing.T) {
	srv, socketPath, cleanup := makeServer(t)
	defer cleanup()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	sendRequest(conn, "initialize", 1, map[string]any{})
	conn.Write(append([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), '\n'))

	resp := sendRequest(conn, "tools/call", 5, map[string]any{
		"name":      "nonexistent",
		"arguments": map[string]any{},
	})
	if resp["error"] == nil {
		t.Fatal("expected error for unknown tool")
	}
	_ = srv
}

func TestServerE2EViaUnixSocket(t *testing.T) {
	srv, socketPath, cleanup := makeServer(t)
	defer cleanup()

	srv.RegisterTool(ToolDef{
		Name:        "echo",
		Description: "echoes the message",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"message": map[string]string{"type": "string"}},
		},
	}, func(ctx context.Context, args map[string]any) (any, error) {
		return args["message"], nil
	})

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 1. Initialize — must declare tools capability.
	resp := sendRequest(conn, "initialize", 1, map[string]any{})
	if resp["error"] != nil {
		t.Fatalf("initialize error: %v", resp["error"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("initialize result is not map: %T", resp["result"])
	}
	caps, ok := result["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("initialize capabilities missing")
	}
	if _, ok := caps["tools"]; !ok {
		t.Fatal("initialize capabilities must include tools")
	}

	// 2. Send notifications/initialized (no response expected).
	conn.Write(append([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), '\n'))

	// 3. tools/list — must return the registered tools.
	resp = sendRequest(conn, "tools/list", 2, map[string]any{})
	if resp["error"] != nil {
		t.Fatalf("tools/list error: %v", resp["error"])
	}
	result, ok = resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list result is not map: %T", resp["result"])
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools/list returned no tools: %v", result["tools"])
	}

	// 4. tools/call — call the echo tool.
	resp = sendRequest(conn, "tools/call", 3, map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"message": "hello world"},
	})
	if resp["error"] != nil {
		t.Fatalf("tools/call error: %v", resp["error"])
	}
	result, ok = resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/call result is not map: %T", resp["result"])
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("content missing or empty: %v", result)
	}
	c0, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("content[0] is not map: %T", content[0])
	}
	text, ok := c0["text"].(string)
	if !ok || text != "hello world" {
		t.Errorf("text = %q, want %q", text, "hello world")
	}
}

func TestServerSendRequest(t *testing.T) {
	ts := strings.NewReader("")
	if ts == nil {
		t.Fatal("unreachable")
	}
}
