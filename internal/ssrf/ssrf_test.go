package ssrf

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func hardened() Policy { return Policy{} }

func TestValidateDestinationScheme(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		pol     Policy
		wantErr string // substring of Detail; "" = want success
	}{
		{"https public ok", "https://hooks.example.com/path", hardened(), ""},
		{"https with query ok", "https://example.com/hook?a=1&b=2", hardened(), ""},
		{"http rejected by default", "http://example.com/hook", hardened(), "scheme \"http\" is not allowed"},
		{"http allowed with override", "http://example.com/hook", Policy{AllowHTTP: true}, ""},
		{"ftp rejected", "ftp://example.com/hook", hardened(), "scheme \"ftp\" is not allowed"},
		{"no scheme rejected", "example.com/hook", hardened(), "scheme"},
		{"userinfo rejected", "https://user:pass@example.com/hook", hardened(), "userinfo"},
		{"fragment rejected", "https://example.com/hook#frag", hardened(), "fragment"},
		{"no host rejected", "https:///path", hardened(), "no host"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDestination(tc.url, tc.pol)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateDestination(%q) unexpected error: %v", tc.url, err)
				}
				return
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("ValidateDestination(%q) = %v, want *Error", tc.url, err)
			}
			if !strings.Contains(e.Detail, tc.wantErr) {
				t.Fatalf("ValidateDestination(%q) detail %q does not contain %q (reason=%s)", tc.url, e.Detail, tc.wantErr, e.Reason)
			}
		})
	}
}

func TestValidateDestinationBlockedIPs(t *testing.T) {
	tests := []struct {
		url      string
		reason   string
		wantFail bool
	}{
		{"https://10.0.0.5/hook", "private_network", true},               // RFC 1918
		{"https://172.16.0.1/hook", "private_network", true},             // RFC 1918
		{"https://192.168.1.1/hook", "private_network", true},            // RFC 1918
		{"https://127.0.0.1/hook", "loopback", true},                     // loopback
		{"https://[::1]/hook", "loopback", true},                         // IPv6 loopback
		{"https://169.254.169.254/hook", "link_local", true},             // cloud metadata
		{"https://169.254.0.1/hook", "link_local", true},                 // link-local
		{"https://[fe80::1]/hook", "link_local", true},                   // IPv6 link-local
		{"https://224.0.0.1/hook", "multicast", true},                    // multicast
		{"https://[ff02::1]/hook", "multicast", true},                    // IPv6 multicast
		{"https://0.0.0.0/hook", "unspecified", true},                    // unspecified
		{"https://100.64.0.1/hook", "shared", true},                      // CGNAT
		{"https://192.0.2.1/hook", "documentation", true},                // TEST-NET-1
		{"https://198.51.100.7/hook", "documentation", true},             // TEST-NET-2
		{"https://203.0.113.9/hook", "documentation", true},              // TEST-NET-3
		{"https://198.18.0.1/hook", "benchmarking", true},                // benchmarking
		{"https://240.0.0.1/hook", "reserved", true},                     // reserved
		{"https://[fc00::1]/hook", "private_network", true},              // IPv6 ULA
		{"https://[2001:db8::1]/hook", "documentation", true},            // IPv6 doc
		{"https://[fec0::1]/hook", "site_local", true},                   // IPv6 site-local
		{"https://[::ffff:10.0.0.5]/hook", "private_network", true},      // v4-mapped v6
		{"https://93.184.216.34/hook", "", false},                        // public IPv4
		{"https://[2606:2800:220:1:248:1893:25c8:1946]/hook", "", false}, // public IPv6
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			err := ValidateDestination(tc.url, hardened())
			if !tc.wantFail {
				if err != nil {
					t.Fatalf("ValidateDestination(%q) unexpected error: %v", tc.url, err)
				}
				return
			}
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("ValidateDestination(%q) = %v, want *Error", tc.url, err)
			}
			if e.Reason != tc.reason {
				t.Fatalf("ValidateDestination(%q) reason = %q, want %q (detail: %s)", tc.url, e.Reason, tc.reason, e.Detail)
			}
		})
	}
}

func TestValidateDestinationBlockedHostnames(t *testing.T) {
	tests := []struct {
		url    string
		reason string
	}{
		{"https://localhost/hook", "localhost"},
		{"https://api.localhost/hook", "localhost"},
		{"https://myhost.local/hook", "mdns"},
		{"https://metadata.google.internal/hook", "metadata"},
		{"https://compute.metadata.google.internal/hook", "metadata"},
	}
	for _, tc := range tests {
		t.Run(tc.url, func(t *testing.T) {
			err := ValidateDestination(tc.url, hardened())
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("ValidateDestination(%q) = %v, want *Error", tc.url, err)
			}
			if e.Reason != tc.reason {
				t.Fatalf("ValidateDestination(%q) reason = %q, want %q (detail: %s)", tc.url, e.Reason, tc.reason, e.Detail)
			}
		})
	}
}

func TestValidateDestinationAllowlistOverrides(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		pol     Policy
		wantErr bool
	}{
		{
			"CIDR allowlist permits private network",
			"https://10.1.2.3/hook",
			Policy{Allowlist: []string{"10.0.0.0/8"}},
			false,
		},
		{
			"exact IP allowlist permits loopback",
			"https://127.0.0.1/hook",
			Policy{Allowlist: []string{"127.0.0.1"}},
			false,
		},
		{
			"AllowPrivate permits loopback dev mode",
			"http://127.0.0.1:8080/hook",
			Policy{AllowHTTP: true, AllowPrivate: true},
			false,
		},
		{
			"hostname allowlist permits metadata hostname",
			"https://metadata.google.internal/hook",
			Policy{Allowlist: []string{"metadata.google.internal"}},
			false,
		},
		{
			"hostname allowlist with leading dot matches subdomain",
			"https://hooks.internal.example/x",
			Policy{Allowlist: []string{".internal.example"}},
			false,
		},
		{
			"allowlist does not widen unrelated networks",
			"https://192.168.1.1/hook",
			Policy{Allowlist: []string{"10.0.0.0/8"}},
			true,
		},
		{
			"AllowPrivate does not permit http without AllowHTTP",
			"http://93.184.216.34/hook",
			Policy{AllowPrivate: true},
			true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDestination(tc.url, tc.pol)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateDestination(%q) expected error, got nil", tc.url)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateDestination(%q) unexpected error: %v", tc.url, err)
			}
		})
	}
}

func TestPolicyValidate(t *testing.T) {
	if err := (Policy{}).Validate(); err != nil {
		t.Fatalf("empty policy should validate: %v", err)
	}
	if err := (Policy{Allowlist: []string{"10.0.0.0/8", "hooks.internal"}}).Validate(); err != nil {
		t.Fatalf("valid allowlist rejected: %v", err)
	}
	for _, bad := range []Policy{
		{Allowlist: []string{""}},
		{Allowlist: []string{"not a cidr or host with spaces"}},
		{Allowlist: []string{"10.0.0.0/99"}},
		{Allowlist: []string{"https://example.com"}},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatalf("malformed policy %+v should fail validation", bad)
		}
	}
}

// fakeResolver returns canned addresses for host without touching the network.
func fakeResolver(addrs map[string][]netip.Addr) func(ctx context.Context, host string) ([]netip.Addr, error) {
	return func(_ context.Context, host string) ([]netip.Addr, error) {
		host = normalizeHost(host)
		if addrs[host] == nil {
			return nil, &net.DNSError{Err: "no such host", Name: host}
		}
		return addrs[host], nil
	}
}

func TestDialerChecksEveryResolvedAddress(t *testing.T) {
	// DNS-rebinding-style resolution: the first answer is public, the second
	// is a private/metadata address. The dialer must reject before dialing
	// (no network I/O happens in this test).
	tests := []struct {
		name   string
		host   string
		addrs  []netip.Addr
		reason string
	}{
		{
			"mixed public and private",
			"rebind.example.com",
			[]netip.Addr{netip.MustParseAddr("93.184.216.34"), netip.MustParseAddr("10.0.0.5")},
			"private_network",
		},
		{
			"metadata endpoint via hostname",
			"metadata.google.internal",
			[]netip.Addr{netip.MustParseAddr("169.254.169.254")},
			"link_local",
		},
		{
			"all private",
			"intranet.internal",
			[]netip.Addr{netip.MustParseAddr("192.168.1.10")},
			"private_network",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := &dialer{
				pol:     compiledPolicy{},
				resolve: fakeResolver(map[string][]netip.Addr{tc.host: tc.addrs}),
				nd:      &net.Dialer{},
			}
			_, err := d.DialContext(context.Background(), "tcp", net.JoinHostPort(tc.host, "443"))
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("DialContext(%q) = %v, want *Error", tc.host, err)
			}
			if e.Reason != tc.reason {
				t.Fatalf("DialContext(%q) reason = %q, want %q (detail: %s)", tc.host, e.Reason, tc.reason, e.Detail)
			}
		})
	}
}

func TestClientEndToEnd(t *testing.T) {
	// Loopback development mode: with the explicit operator overrides the
	// client delivers to a local httptest server; without them the same
	// request is refused before any connection is attempted.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	permissive := NewClient(Policy{AllowHTTP: true, AllowPrivate: true})
	resp, err := permissive.Get(server.URL)
	if err != nil {
		t.Fatalf("permissive client GET %s: %v", server.URL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}

	hardenedClient := NewClient(Policy{})
	_, err = hardenedClient.Get(server.URL)
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("hardened client GET %s = %v, want policy *Error", server.URL, err)
	}
	if e.Reason != "scheme" {
		t.Fatalf("hardened client rejection reason = %q, want scheme (detail: %s)", e.Reason, e.Detail)
	}
}

func TestClientNeverFollowsRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/end", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(Policy{AllowHTTP: true, AllowPrivate: true})
	resp, err := client.Get(server.URL + "/start")
	if err != nil {
		t.Fatalf("GET %s: %v", server.URL, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302 (redirect must not be followed)", resp.StatusCode)
	}
}

func TestHostnameAllowlistBypassesRangeCheckAtDial(t *testing.T) {
	// An operator-allowlisted hostname is trusted: its resolved address skips
	// the range check, but the address is resolved once and dialed directly.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	host, portStr, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatalf("split listener addr: %v", err)
	}
	ip := netip.MustParseAddr(host)
	resolver := fakeResolver(map[string][]netip.Addr{"trusted.internal": {ip}})

	// Allowlisted hostname: the loopback range check is bypassed and the dial
	// reaches the local listener.
	allowed := &dialer{
		pol:     compiledPolicy{allowHosts: []string{"trusted.internal"}},
		resolve: resolver,
		nd:      &net.Dialer{},
	}
	conn, err := allowed.DialContext(context.Background(), "tcp", net.JoinHostPort("trusted.internal", portStr))
	if err != nil {
		t.Fatalf("DialContext with allowlisted hostname: %v", err)
	}
	conn.Close()

	// Same resolution without the allowlist: refused before any dial.
	blocked := &dialer{
		pol:     compiledPolicy{},
		resolve: resolver,
		nd:      &net.Dialer{},
	}
	_, err = blocked.DialContext(context.Background(), "tcp", net.JoinHostPort("trusted.internal", portStr))
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("DialContext without allowlist = %v, want policy *Error", err)
	}
	if e.Reason != "loopback" {
		t.Fatalf("rejection reason = %q, want loopback", e.Reason)
	}
}
