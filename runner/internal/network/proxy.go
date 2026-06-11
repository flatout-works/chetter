// Package network provides transparent HTTP proxying, DNS filtering, and
// per-task Linux bridge + iptables network isolation for agent containers.
package network

import (
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/elazarl/goproxy"
)

// TransparentProxy wraps goproxy with domain-based allowlist and blocklist
// filtering for all HTTP/HTTPS traffic.
type TransparentProxy struct {
	Addr           string
	AllowedDomains []string
	BlockedDomains []string
	Server         *goproxy.ProxyHttpServer
	httpServer     *http.Server
	listener       net.Listener
}

// NewProxy creates a transparent HTTP proxy.
func NewProxy(addr string, allowed, blocked []string) *TransparentProxy {
	p := &TransparentProxy{
		Addr:           addr,
		AllowedDomains: allowed,
		BlockedDomains: blocked,
		Server:         goproxy.NewProxyHttpServer(),
	}
	p.Server.Verbose = false
	p.Server.OnRequest().DoFunc(p.handleRequest)
	return p
}

func (p *TransparentProxy) handleRequest(req *http.Request, ctx *goproxy.ProxyCtx) (*http.Request, *http.Response) {
	host := req.Host
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	slog.Info("request", "component", "proxy", "method", req.Method, "url", req.URL.String(), "host", host)

	if isBlocked(host, p.BlockedDomains) {
		slog.Warn("BLOCKED", "component", "proxy", "host", host)
		return req, goproxy.NewResponse(req, goproxy.ContentTypeText, http.StatusForbidden, "Domain blocked by runner policy")
	}

	if len(p.AllowedDomains) > 0 && !p.isAllowed(host) {
		slog.Warn("NOT ALLOWED", "component", "proxy", "host", host)
		return req, goproxy.NewResponse(req, goproxy.ContentTypeText, http.StatusForbidden, "Domain not in runner allowlist")
	}

	normalizeTransparentHTTP(req)
	return req, nil
}

func normalizeTransparentHTTP(req *http.Request) {
	if req.URL.Scheme != "" || req.Host == "" {
		return
	}
	req.URL.Scheme = "http"
	req.URL.Host = req.Host
}

func (p *TransparentProxy) isAllowed(host string) bool {
	return domainMatches(host, p.AllowedDomains)
}

// Start begins listening.
func (p *TransparentProxy) Start() error {
	slog.Info("starting", "component", "proxy", "addr", p.Addr)
	var err error
	p.listener, err = net.Listen("tcp", p.Addr)
	if err != nil {
		return err
	}
	p.httpServer = &http.Server{Handler: p.Server, Addr: p.Addr}
	return p.httpServer.Serve(p.listener)
}

// Stop shuts down the proxy server.
func (p *TransparentProxy) Stop() error {
	if p.httpServer != nil {
		return p.httpServer.Close()
	}
	return nil
}
