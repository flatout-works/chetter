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
	sdkServer  *mcplib.Server
	httpSrv    *http.Server
	mux        *http.ServeMux
	addr       string
	token      string
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	closeOnce  sync.Once
	closeMu    sync.Mutex
	closed     bool
	closeErr   error
	closeHooks []func()

	credentialMu  sync.Mutex
	credentialSet bool
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

// NewServer creates an authenticated MCP server on a random TCP port. The
// optional listenHost narrows which local interface accepts task traffic; it
// defaults to loopback for direct callers and tests.
func NewServer(listenHost ...string) (*Server, error) {
	host := "127.0.0.1"
	if len(listenHost) > 0 && listenHost[0] != "" {
		host = listenHost[0]
	}
	token, err := randomBearerToken()
	if err != nil {
		return nil, fmt.Errorf("generate MCP bearer token: %w", err)
	}
	ln, err := net.Listen("tcp4", net.JoinHostPort(host, "0")) // tcp4 to avoid IPv6 which gVisor can't reach
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", host, err)
	}
	addr := ln.Addr().String()

	sdkServer := mcplib.NewServer(&mcplib.Implementation{Name: "chetter-runner", Version: "0.1.0"}, nil)
	getServer := func(_ *http.Request) *mcplib.Server { return sdkServer }
	mcpHandler := mcplib.NewStreamableHTTPHandler(getServer, &mcplib.StreamableHTTPOptions{Stateless: true, JSONResponse: true})

	mux := http.NewServeMux()
	mux.Handle("/mcp", requireBearerToken(token, mcpHandler))

	httpSrv := &http.Server{Handler: mux}
	s := &Server{sdkServer: sdkServer, httpSrv: httpSrv, mux: mux, addr: addr, token: token}
	serverCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.httpSrv.BaseContext = func(net.Listener) context.Context { return serverCtx }

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		if err := httpSrv.Serve(ln); err != http.ErrServerClosed {
			slog.Error("mcp server error", "err", err)
		}
	}()
	return s, nil
}

// Addr returns the listen address for the MCP server.
func (s *Server) Addr() string { return s.addr }

// Token returns the per-server bearer token used by the task's private MCP
// configuration and credential endpoint.
func (s *Server) Token() string { return s.token }

// SetCredentialHandler enables the private, non-MCP credential endpoint. It
// reuses the execution's MCP bearer capability and accepts no task-controlled
// identity fields.
func (s *Server) SetCredentialHandler(get CredentialHandler) error {
	if get == nil {
		return fmt.Errorf("credential handler is required")
	}
	s.credentialMu.Lock()
	defer s.credentialMu.Unlock()
	if s.credentialSet {
		return fmt.Errorf("credential handler is already registered")
	}
	handler := requireBearerToken(s.token, githubCredentialHandler(get))
	s.mux.Handle(GitHubCredentialPath, noStore(handler))
	s.credentialSet = true
	return nil
}

// RegisterTool registers a named tool with its definition and handler.
func (s *Server) RegisterTool(def ToolDef, handler ToolHandler) {
	s.sdkServer.AddTool(&mcplib.Tool{
		Name:        def.Name,
		Description: def.Description,
		InputSchema: def.InputSchema,
	}, adaptHandler(handler))
}

func githubCredentialHandler(get CredentialHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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

// AddCloseHook registers cleanup that runs once after the HTTP server exits.
// A hook added after closure runs immediately.
func (s *Server) AddCloseHook(hook func()) {
	if hook == nil {
		return
	}
	s.closeMu.Lock()
	if s.closed {
		s.closeMu.Unlock()
		hook()
		return
	}
	s.closeHooks = append(s.closeHooks, hook)
	s.closeMu.Unlock()
}

// Close shuts down the HTTP server and runs registered cleanup hooks. It is
// safe to call more than once.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		s.closeErr = s.httpSrv.Shutdown(shutdownCtx)
		cancel()
		s.wg.Wait()

		s.closeMu.Lock()
		s.closed = true
		hooks := append([]func(){}, s.closeHooks...)
		s.closeHooks = nil
		s.closeMu.Unlock()
		for _, hook := range hooks {
			hook()
		}
	})
	return s.closeErr
}

func randomBearerToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func requireBearerToken(token string, next http.Handler) http.Handler {
	expected := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := []byte(r.Header.Get("Authorization"))
		if len(provided) != len(expected) || subtle.ConstantTimeCompare(provided, expected) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
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
