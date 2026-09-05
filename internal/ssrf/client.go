package ssrf

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// Timeouts applied by the dedicated outbound webhook client. The caller's
// context (event callback dispatch bounds delivery with a timeout) can only
// shorten these; total client time is min(clientTimeout, ctx deadline).
const (
	dialTimeout           = 10 * time.Second
	tlsHandshakeTimeout   = 10 * time.Second
	responseHeaderTimeout = 15 * time.Second
	clientTimeout         = 30 * time.Second
)

// dialer resolves the destination host once, checks every resolved address
// against the policy, and dials a validated address directly so a DNS
// rebinding between validation and dial cannot reach a blocked address. The
// transport performs TLS with the request hostname, so SNI and certificate
// verification still match the configured destination.
type dialer struct {
	pol        compiledPolicy
	compileErr error
	resolve    func(ctx context.Context, host string) ([]netip.Addr, error)
	nd         *net.Dialer
}

func (d *dialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if d.compileErr != nil {
		return nil, &Error{Reason: "policy_invalid", Detail: d.compileErr.Error()}
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, &Error{Reason: "invalid_url", Detail: fmt.Sprintf("invalid dial address %q", addr)}
	}
	if host == "" {
		return nil, &Error{Reason: "invalid_url", Detail: "empty destination host"}
	}
	ips, err := d.resolve(ctx, host)
	if err != nil {
		return nil, &Error{Reason: "resolution", Detail: fmt.Sprintf("cannot resolve destination host %q: %v", host, err)}
	}
	if len(ips) == 0 {
		return nil, &Error{Reason: "resolution", Detail: fmt.Sprintf("destination host %q resolved to no addresses", host)}
	}
	// Every resolved address is checked (not just the first) so a poisoned or
	// rebinding DNS response that mixes public and blocked addresses cannot
	// slip through. Operator-allowlisted hostnames are trusted explicitly.
	if !d.pol.hostAllowed(host) {
		for _, ip := range ips {
			if err := checkAddr(ip, d.pol); err != nil {
				return nil, err
			}
		}
	}
	return d.nd.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
}

func resolveAddrs(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, ok := parseIPLiteral(host); ok {
		return []netip.Addr{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		if ip, ok := netip.AddrFromSlice(a.IP); ok {
			out = append(out, ip.Unmap())
		}
	}
	return out, nil
}

// policyTripper re-validates every request URL against the policy before any
// network I/O, so callers that forget an explicit ValidateDestination still
// fail closed.
type policyTripper struct {
	cp   compiledPolicy
	err  error
	next http.RoundTripper
}

func (t *policyTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.err != nil {
		return nil, &Error{Reason: "policy_invalid", Detail: t.err.Error()}
	}
	if req.URL == nil {
		return nil, &Error{Reason: "invalid_url", Detail: "request has no URL"}
	}
	if err := checkURL(req.URL, t.cp); err != nil {
		return nil, err
	}
	return t.next.RoundTrip(req)
}

// NewClient returns an http.Client that enforces pol on every request: the
// request URL is validated before any network I/O, every address actually
// dialed is checked against the policy (no DNS-rebinding window), proxy
// environment variables are never consulted, redirects are never followed,
// and connect/TLS/header/total timeouts are bounded. All outbound webhook
// delivery from the control plane must use this client instead of
// http.DefaultClient (issue #337).
func NewClient(pol Policy) *http.Client {
	cp, err := pol.compile()
	d := &dialer{
		pol:        cp,
		compileErr: err,
		resolve:    resolveAddrs,
		nd:         &net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second},
	}
	transport := &http.Transport{
		// Never honor HTTP_PROXY/HTTPS_PROXY/ALL_PROXY: honoring ambient
		// proxy configuration would route destinations through an
		// operator-uncontrolled hop.
		Proxy:                 nil,
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	roundTripper := &policyTripper{cp: cp, err: err, next: transport}
	return &http.Client{
		Transport: roundTripper,
		Timeout:   clientTimeout,
		// Never follow redirects: a redirect target was not the configured,
		// policy-checked destination (and would receive the callback headers).
		// Delivery reports the redirect status as a non-2xx failure instead.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
