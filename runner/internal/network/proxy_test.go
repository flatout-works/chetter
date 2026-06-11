package network

import (
	"net/http"
	"testing"
)

func TestProxyPolicyMatching(t *testing.T) {
	p := NewProxy(":0", []string{"github.com", "pkg.go.dev"}, []string{"pastebin.com", "webhook.site"})

	for _, host := range []string{"github.com", "raw.github.com", "pkg.go.dev", "sub.pkg.go.dev"} {
		if !p.isAllowed(host) {
			t.Fatalf("host %q should be allowed", host)
		}
	}
	for _, host := range []string{"pastebin.com", "x.pastebin.com", "webhook.site"} {
		if !isBlocked(host, p.BlockedDomains) {
			t.Fatalf("host %q should be blocked", host)
		}
	}
	if p.isAllowed("example.com") {
		t.Fatal("example.com should not be allowed")
	}
}

func TestProxyHandleRequestBlocksPolicyViolations(t *testing.T) {
	p := NewProxy(":0", []string{"github.com"}, []string{"pastebin.com"})

	for _, tc := range []struct {
		name string
		host string
		want int
	}{
		{name: "allowed", host: "github.com", want: 0},
		{name: "allowed subdomain", host: "raw.github.com", want: 0},
		{name: "blocked", host: "pastebin.com", want: http.StatusForbidden},
		{name: "not allowlisted", host: "example.com", want: http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "http://"+tc.host+"/", nil)
			if err != nil {
				t.Fatal(err)
			}
			_, resp := p.handleRequest(req, nil)
			if tc.want == 0 {
				if resp != nil {
					t.Fatalf("response = %v, want nil", resp)
				}
				return
			}
			if resp == nil || resp.StatusCode != tc.want {
				t.Fatalf("status = %v, want %d", resp, tc.want)
			}
		})
	}
}

func TestNormalizeTransparentHTTP(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "/path", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "example.com"

	normalizeTransparentHTTP(req)

	if req.URL.Scheme != "http" {
		t.Fatalf("scheme = %q, want http", req.URL.Scheme)
	}
	if req.URL.Host != "example.com" {
		t.Fatalf("host = %q, want example.com", req.URL.Host)
	}
	if req.URL.Path != "/path" {
		t.Fatalf("path = %q, want /path", req.URL.Path)
	}
}
