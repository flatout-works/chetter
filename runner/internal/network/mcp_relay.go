package network

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
)

// MCPRelay exposes a runner-local MCP endpoint that forwards requests to the
// configured Chetter MCP service. Task containers reach the relay by runner IP,
// avoiding Docker service-name DNS inside gVisor.
type MCPRelay struct {
	listenAddr string
	target     *url.URL
	authToken  string
	server     *http.Server
	listener   net.Listener
	mu         sync.Mutex
	claims     map[[sha256.Size]byte]relayClaim
	rejected   atomic.Uint64
}

type relayClaim struct {
	TaskID      string
	ExecutionID string
}

type relayClaimContextKey struct{}

// NewMCPRelay creates a relay for targetURL.
func NewMCPRelay(listenAddr, targetURL, authToken string) (*MCPRelay, error) {
	target, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("parse relay target: %w", err)
	}
	if target.Scheme != "http" && target.Scheme != "https" {
		return nil, fmt.Errorf("relay target must use http or https")
	}
	if target.Host == "" {
		return nil, fmt.Errorf("relay target host is required")
	}
	return &MCPRelay{
		listenAddr: listenAddr,
		target:     target,
		authToken:  authToken,
		claims:     make(map[[sha256.Size]byte]relayClaim),
	}, nil
}

// Start begins serving the relay endpoint.
func (r *MCPRelay) Start() error {
	listener, err := net.Listen("tcp", r.listenAddr)
	if err != nil {
		return err
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil // The runner reaches Docker/Kubernetes services directly.
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(req *httputil.ProxyRequest) {
			req.SetURL(r.target)
			req.Out.URL.Path = r.target.Path
			req.Out.URL.RawPath = r.target.EscapedPath()
			req.Out.URL.RawQuery = r.target.RawQuery
			req.Out.Host = r.target.Host
			req.Out.Header.Del("Authorization")
			if r.authToken != "" {
				req.Out.Header.Set("Authorization", "Bearer "+r.authToken)
			}
			if claim, ok := req.In.Context().Value(relayClaimContextKey{}).(relayClaim); ok {
				req.Out.Header.Set("X-Chetter-Claim-ID", claim.ExecutionID)
				req.Out.Header.Set("X-Chetter-Task-ID", claim.TaskID)
			}
		},
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", r.requireClaim(proxy))
	server := &http.Server{Handler: mux}

	r.mu.Lock()
	r.listener = listener
	r.server = server
	r.mu.Unlock()

	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			slog.Error("MCP relay stopped", "err", err)
		}
	}()
	return nil
}

// RegisterClaim authorizes one active execution to use the relay. The returned
// function removes that capability and is safe to call more than once.
func (r *MCPRelay) RegisterClaim(token, taskID, executionID string) (func(), error) {
	if token == "" || taskID == "" || executionID == "" {
		return nil, fmt.Errorf("relay claim token, task ID, and execution ID are required")
	}
	digest := sha256.Sum256([]byte(token))
	r.mu.Lock()
	if _, exists := r.claims[digest]; exists {
		r.mu.Unlock()
		return nil, fmt.Errorf("relay claim token is already registered")
	}
	r.claims[digest] = relayClaim{TaskID: taskID, ExecutionID: executionID}
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.claims, digest)
			r.mu.Unlock()
		})
	}, nil
}

// RejectedRequests returns the number of requests rejected before proxying.
func (r *MCPRelay) RejectedRequests() uint64 { return r.rejected.Load() }

func (r *MCPRelay) requireClaim(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		const prefix = "Bearer "
		authorization := req.Header.Get("Authorization")
		if !strings.HasPrefix(authorization, prefix) {
			r.reject(w, req)
			return
		}
		digest := sha256.Sum256([]byte(strings.TrimPrefix(authorization, prefix)))
		r.mu.Lock()
		claim, ok := r.claims[digest]
		r.mu.Unlock()
		if !ok {
			r.reject(w, req)
			return
		}
		next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), relayClaimContextKey{}, claim)))
	})
}

func (r *MCPRelay) reject(w http.ResponseWriter, req *http.Request) {
	r.rejected.Add(1)
	slog.Warn("rejected unauthorized MCP relay request", "remote_addr", req.RemoteAddr)
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "unauthorized", http.StatusUnauthorized)
}

// Addr returns the listening address, or an empty string before Start.
func (r *MCPRelay) Addr() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.listener == nil {
		return ""
	}
	return r.listener.Addr().String()
}

// Stop shuts down the relay.
func (r *MCPRelay) Stop() error {
	r.mu.Lock()
	server := r.server
	r.mu.Unlock()
	if server == nil {
		return nil
	}
	return server.Close()
}
