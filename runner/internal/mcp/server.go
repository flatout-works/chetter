// Package mcp exposes the MCP (Model Context Protocol) server for the
// runner. Each task gets its own MCP server instance that registers tools
// (GitHub operations) and serves them over HTTP Streamable transport.
//
// A random TCP port is allocated per task, and the agent container connects
// via a remote URL. This avoids gVisor Unix socket incompatibility.
package mcp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"

	mcplib "github.com/modelcontextprotocol/go-sdk/mcp"
)

type Server struct {
	sdkServer *mcplib.Server
	httpSrv   *http.Server
	addr      string
	authToken string
	wg        sync.WaitGroup
}

// ToolHandler is the function signature for tool implementations.
type ToolHandler func(ctx context.Context, args map[string]any) (any, error)

// ToolDef describes a tool with its name, description, and input JSON schema.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// NewServer creates a new MCP server listening on a random TCP port.
func NewServer() (*Server, error) {
	authToken, err := randomAuthToken()
	if err != nil {
		return nil, err
	}

	ln, err := net.Listen("tcp4", "0.0.0.0:0") // tcp4 to avoid IPv6 which gVisor can't reach
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	addr := ln.Addr().String()

	sdkServer := mcplib.NewServer(&mcplib.Implementation{Name: "chetter-runner", Version: "0.1.0"}, nil)

	getServer := func(_ *http.Request) *mcplib.Server { return sdkServer }
	handler := mcplib.NewStreamableHTTPHandler(getServer, &mcplib.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	mux := http.NewServeMux()
	mux.Handle("/mcp", bearerAuth(authToken, handler))

	httpSrv := &http.Server{Handler: mux}
	s := &Server{sdkServer: sdkServer, httpSrv: httpSrv, addr: addr, authToken: authToken}
	s.httpSrv.BaseContext = func(ln net.Listener) context.Context { return context.WithoutCancel(context.Background()) }

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := httpSrv.Serve(ln); err != http.ErrServerClosed {
			slog.Error("mcp server error", "err", err)
		}
	}()
	return s, nil
}

// Addr returns the listen address for the MCP server (e.g. "127.0.0.1:12345").
func (s *Server) Addr() string { return s.addr }

// AuthToken returns the per-server bearer token required for MCP requests.
func (s *Server) AuthToken() string { return s.authToken }

// RegisterTool registers a named tool with its definition and handler.
func (s *Server) RegisterTool(def ToolDef, handler ToolHandler) {
	s.sdkServer.AddTool(&mcplib.Tool{
		Name:        def.Name,
		Description: def.Description,
		InputSchema: def.InputSchema,
	}, adaptHandler(handler))
}

// Close shuts down the HTTP server.
func (s *Server) Close() error {
	if err := s.httpSrv.Shutdown(context.Background()); err != nil {
		return err
	}
	s.wg.Wait()
	return nil
}

func adaptHandler(h ToolHandler) mcplib.ToolHandler {
	return func(ctx context.Context, req *mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		var args map[string]any
		if req.Params.Arguments != nil {
			args = make(map[string]any)
			if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
				var res mcplib.CallToolResult
				res.SetError(fmt.Errorf("invalid arguments: %w", err))
				return &res, nil
			}
		}
		if args == nil {
			args = make(map[string]any)
		}
		result, err := h(ctx, args)
		if err != nil {
			var res mcplib.CallToolResult
			res.SetError(err)
			return &res, nil
		}
		text := fmt.Sprintf("%v", result)
		if s, ok := result.(string); ok {
			text = s
		}
		return &mcplib.CallToolResult{
			Content: []mcplib.Content{&mcplib.TextContent{Text: text}},
		}, nil
	}
}

func randomAuthToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate mcp auth token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

func bearerAuth(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validBearer(r.Header.Get("Authorization"), token) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func validBearer(header, token string) bool {
	const prefix = "Bearer "
	if token == "" || len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return false
	}
	got := header[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}
