package network

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
)

// MCPRelay exposes a runner-local MCP endpoint that forwards requests to the
// configured Chetter MCP service. Task containers reach the relay by runner IP,
// avoiding Docker service-name DNS inside gVisor.
type MCPRelay struct {
	listenAddr string
	target     *url.URL
	server     *http.Server
	listener   net.Listener
	mu         sync.Mutex
}

// NewMCPRelay creates a relay for targetURL.
func NewMCPRelay(listenAddr, targetURL string) (*MCPRelay, error) {
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
	return &MCPRelay{listenAddr: listenAddr, target: target}, nil
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
		},
	}
	mux := http.NewServeMux()
	mux.Handle("/mcp", proxy)
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
