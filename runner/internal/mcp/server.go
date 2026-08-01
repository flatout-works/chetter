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
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	mcplib "github.com/modelcontextprotocol/go-sdk/mcp"
)

const GitHubCredentialPath = "/internal/github-credential"

type Server struct {
	sdkServer            *mcplib.Server
	httpSrv              *http.Server
	addr                 string
	cancel               context.CancelFunc
	wg                   sync.WaitGroup
	credentialCapability string
}

// ToolHandler is the function signature for tool implementations.
type ToolHandler func(ctx context.Context, args map[string]any) (any, error)

// CredentialHandler returns a current credential for the execution captured
// by its closure.
type CredentialHandler func(ctx context.Context) (string, error)

// ToolDef describes a tool with its name, description, and input JSON schema.
type ToolDef struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// NewServer creates a new MCP server listening on a random TCP port.
func NewServer(credentialHandlers ...CredentialHandler) (*Server, error) {
	ln, err := net.Listen("tcp4", "0.0.0.0:0") // tcp4 to avoid IPv6 which gVisor can't reach
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	addr := ln.Addr().String()

	sdkServer := mcplib.NewServer(&mcplib.Implementation{Name: "chetter-runner", Version: "0.1.0"}, nil)

	getServer := func(_ *http.Request) *mcplib.Server { return sdkServer }
	handler := mcplib.NewStreamableHTTPHandler(getServer, &mcplib.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	var credentialCapability string
	if len(credentialHandlers) > 0 && credentialHandlers[0] != nil {
		capabilityBytes := make([]byte, 32)
		if _, err := rand.Read(capabilityBytes); err != nil {
			_ = ln.Close()
			return nil, fmt.Errorf("generate credential capability: %w", err)
		}
		credentialCapability = base64.RawURLEncoding.EncodeToString(capabilityBytes)
		mux.Handle(GitHubCredentialPath, githubCredentialHandler(credentialCapability, credentialHandlers[0]))
	}

	httpSrv := &http.Server{Handler: mux}
	s := &Server{sdkServer: sdkServer, httpSrv: httpSrv, addr: addr, credentialCapability: credentialCapability}
	serverCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.httpSrv.BaseContext = func(ln net.Listener) context.Context { return serverCtx }

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

// CredentialCapability returns the per-server bearer capability, or an empty
// string when the private credential endpoint is disabled.
func (s *Server) CredentialCapability() string { return s.credentialCapability }

// RegisterTool registers a named tool with its definition and handler.
func (s *Server) RegisterTool(def ToolDef, handler ToolHandler) {
	s.sdkServer.AddTool(&mcplib.Tool{
		Name:        def.Name,
		Description: def.Description,
		InputSchema: def.InputSchema,
	}, adaptHandler(handler))
}

func githubCredentialHandler(capability string, get CredentialHandler) http.Handler {
	expected := sha256.Sum256([]byte("Bearer " + capability))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		actual := sha256.Sum256([]byte(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1))
		if err != nil || len(body) != 0 {
			http.Error(w, "request body is not accepted", http.StatusBadRequest)
			return
		}
		token, err := get(r.Context())
		if err != nil || token == "" {
			http.Error(w, "credential unavailable", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, token)
	})
}

// Close shuts down the HTTP server.
func (s *Server) Close() error {
	s.cancel()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.httpSrv.Shutdown(shutdownCtx); err != nil {
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
