// Package mcp implements a minimal JSON-RPC 2.0 MCP (Model Context Protocol)
// server over a Unix domain socket. Each task gets its own MCP server instance
// that exposes tools (workspace I/O, git, NATS, fetch, result reporting) to
// agent processes running inside the task environment.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
)

// Server implements an MCP JSON-RPC 2.0 server over a Unix domain socket.
type Server struct {
	socketPath string
	tools      map[string]ToolHandler
	listener   net.Listener
}

// ToolHandler is a function that handles a tool call with the given arguments.
type ToolHandler func(ctx context.Context, args map[string]any) (any, error)

// NewServer creates a new MCP server listening on the given Unix socket path.
func NewServer(socketPath string) (*Server, error) {
	if err := os.MkdirAll(filepath.Dir(socketPath), 0750); err != nil {
		return nil, fmt.Errorf("create socket dir: %w", err)
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("remove old socket: %w", err)
	}

	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on socket: %w", err)
	}

	return &Server{
		socketPath: socketPath,
		tools:      make(map[string]ToolHandler),
		listener:   l,
	}, nil
}

// RegisterTool registers a named tool handler that can be invoked via tools/call.
func (s *Server) RegisterTool(name string, handler ToolHandler) {
	s.tools[name] = handler
}

// Serve accepts connections until the context is cancelled. Each connection is
// handled in its own goroutine.
func (s *Server) Serve(ctx context.Context) error {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				continue
			}
		}
		go s.handleConn(ctx, conn)
	}
}

// Close closes the Unix socket listener. After Close, Serve returns.
func (s *Server) Close() error {
	return s.listener.Close()
}

// handleConn processes one MCP client connection. Each line is a JSON-RPC
// request; responses are written back as newline-delimited JSON.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		var req JSONRPCRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			s.writeError(conn, nil, -32700, "Parse error")
			continue
		}

		resp := s.handleRequest(ctx, &req)
		if resp != nil {
			data, _ := json.Marshal(resp)
			fmt.Fprintf(conn, "%s\n", data)
		}
	}
}

func (s *Server) handleRequest(ctx context.Context, req *JSONRPCRequest) *JSONRPCResponse {
	if req.JSONRPC != "2.0" {
		return s.errorResp(req.ID, -32600, "Invalid Request")
	}

	switch req.Method {
	case "initialize":
		return s.resultResp(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"serverInfo": map[string]string{
				"name":    "devfleet-runner",
				"version": "0.1.0",
			},
		})

	case "tools/list":
		return s.resultResp(req.ID, map[string]any{"tools": ToolDefinitions()})

	case "tools/call":
		name, ok := req.Params["name"].(string)
		if !ok {
			return s.errorResp(req.ID, -32602, "Invalid params: missing name")
		}
		args, _ := req.Params["arguments"].(map[string]any)
		if args == nil {
			args = make(map[string]any)
		}

		handler, ok := s.tools[name]
		if !ok {
			return s.errorResp(req.ID, -32601, "Tool not found: "+name)
		}

		result, err := handler(ctx, args)
		if err != nil {
			slog.Error("tool error", "name", name, "err", err)
			return s.resultResp(req.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": err.Error()}},
				"isError": true,
			})
		}

		content := fmt.Sprintf("%v", result)
		if s, ok := result.(string); ok {
			content = s
		}
		return s.resultResp(req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": content}},
		})

	default:
		return s.errorResp(req.ID, -32601, "Method not found")
	}
}

func (s *Server) errorResp(id any, code int, message string) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &JSONRPCError{Code: code, Message: message},
	}
}

func (s *Server) resultResp(id any, result any) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}
}

func (s *Server) writeError(conn net.Conn, id any, code int, message string) {
	resp := s.errorResp(id, code, message)
	data, _ := json.Marshal(resp)
	fmt.Fprintf(conn, "%s\n", data)
}

// JSONRPCRequest is a JSON-RPC 2.0 request message.
type JSONRPCRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params,omitempty"`
}

// JSONRPCResponse is a JSON-RPC 2.0 response message.
type JSONRPCResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  any            `json:"result,omitempty"`
	Error   *JSONRPCError  `json:"error,omitempty"`
}

// JSONRPCError is a JSON-RPC 2.0 error.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}
