package network

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func TestDNSPolicyMatching(t *testing.T) {
	d := NewDNSProxy(":0", "8.8.8.8:53", []string{"chetter-mcp", "github.com"}, []string{"metadata.google.internal", "blocked.test"}, nil)

	for _, name := range []string{"metadata.google.internal", "a.metadata.google.internal", "blocked.test", "sub.blocked.test"} {
		if !isBlocked(name, d.BlockedDomains) {
			t.Fatalf("name %q should be blocked", name)
		}
	}
	if isBlocked("github.com", d.BlockedDomains) {
		t.Fatal("github.com should not be blocked")
	}
	if !domainMatches("api.github.com", d.AllowedDomains) {
		t.Fatal("api.github.com should be allowed")
	}
	if domainMatches("example.com", d.AllowedDomains) {
		t.Fatal("example.com should not be allowed")
	}
}

func TestDNSProxyRequiresUpstream(t *testing.T) {
	proxy := NewDNSProxy("127.0.0.1:0", "", nil, nil, nil)
	if err := proxy.Start(); err == nil {
		t.Fatal("expected empty upstream to fail startup")
	}
}

func TestDNSProxyServesStaticRecordsAndForwards(t *testing.T) {
	upstreamConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen upstream: %v", err)
	}
	upstreamMux := dns.NewServeMux()
	upstreamMux.HandleFunc(".", func(w dns.ResponseWriter, r *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(r)
		response.Answer = []dns.RR{
			&dns.A{
				Hdr: dns.RR_Header{Name: r.Question[0].Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
				A:   net.IPv4(198, 51, 100, 7),
			},
		}
		_ = w.WriteMsg(response)
	})
	upstream := &dns.Server{PacketConn: upstreamConn, Handler: upstreamMux}
	go func() { _ = upstream.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = upstream.Shutdown()
	})

	proxy := NewDNSProxy(
		"127.0.0.1:0",
		upstreamConn.LocalAddr().String(),
		nil,
		nil,
		map[string][]net.IP{"chetter-mcp": {net.IPv4(192, 0, 2, 10)}},
	)
	if err := proxy.Start(); err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	t.Cleanup(func() {
		_ = proxy.Stop()
	})

	query := func(name string, qtype uint16, network string) *dns.Msg {
		t.Helper()
		client := &dns.Client{Net: network, Timeout: 2 * time.Second}
		question := new(dns.Msg)
		question.SetQuestion(dns.Fqdn(name), qtype)
		response, _, err := client.Exchange(question, proxy.Addr())
		if err != nil {
			t.Fatalf("query %s/%s over %s: %v", name, dns.TypeToString[qtype], network, err)
		}
		return response
	}

	staticResponse := query("chetter-mcp", dns.TypeA, "tcp")
	if len(staticResponse.Answer) != 1 || staticResponse.Answer[0].(*dns.A).A.String() != "192.0.2.10" {
		t.Fatalf("unexpected static response: %v", staticResponse.Answer)
	}

	forwardedResponse := query("forward.test", dns.TypeA, "udp")
	if len(forwardedResponse.Answer) != 1 || forwardedResponse.Answer[0].(*dns.A).A.String() != "198.51.100.7" {
		t.Fatalf("unexpected forwarded response: %v", forwardedResponse.Answer)
	}

	noIPv6Response := query("chetter-mcp", dns.TypeAAAA, "udp")
	if noIPv6Response.Rcode != dns.RcodeSuccess || len(noIPv6Response.Answer) != 0 {
		t.Fatalf("unexpected AAAA response: %+v", noIPv6Response)
	}
}
