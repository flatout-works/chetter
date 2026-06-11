package network

import (
	"log/slog"
	"strings"

	"github.com/flatout-works/devfleet/runner/internal/config"
	"github.com/miekg/dns"
)

// DNSProxy is a UDP DNS forwarder that blocks forbidden domains and
// suppresses AAAA (IPv6) responses to avoid stalls inside Kata VMs.
type DNSProxy struct {
	ListenAddr     string
	Upstream       string
	BlockedDomains []string
	server         *dns.Server
}

// NewDNSProxy creates a DNS proxy.
func NewDNSProxy(listenAddr, upstream string, blocked []string) *DNSProxy {
	if upstream == "" {
		upstream = config.DefaultDNSUpstream
	}
	return &DNSProxy{
		ListenAddr:     listenAddr,
		Upstream:       upstream,
		BlockedDomains: blocked,
	}
}

// Start begins serving DNS requests.
func (d *DNSProxy) Start() error {
	dns.HandleFunc(".", d.handleRequest)
	d.server = &dns.Server{Addr: d.ListenAddr, Net: "udp"}
	slog.Info("starting", "component", "dns", "addr", d.ListenAddr, "upstream", d.Upstream)
	return d.server.ListenAndServe()
}

// Stop shuts down the DNS server.
func (d *DNSProxy) Stop() error {
	if d.server != nil {
		return d.server.Shutdown()
	}
	return nil
}

func (d *DNSProxy) handleRequest(w dns.ResponseWriter, r *dns.Msg) {
	if len(r.Question) == 0 {
		dns.HandleFailed(w, r)
		return
	}

	q := r.Question[0]
	name := strings.TrimSuffix(q.Name, ".")

	// Strip AAAA records to force IPv4-only and avoid Happy Eyeballs
	// stalls inside Kata where IPv6 is un-routed. Return NOERROR with
	// an empty answer section so resolvers don't treat it as a cacheable
	// negative for all record types (RFC 2308).
	if q.Qtype == dns.TypeAAAA {
		m := new(dns.Msg)
		m.SetReply(r)
		m.Answer = []dns.RR{}
		m.Ns = []dns.RR{}
		m.Extra = []dns.RR{}
		m.Rcode = dns.RcodeSuccess
		slog.Debug("empty-AAAA", "component", "dns", "name", name)
		w.WriteMsg(m)
		return
	}

	if isBlocked(name, d.BlockedDomains) {
		slog.Warn("BLOCKED", "component", "dns", "name", name)
		m := new(dns.Msg)
		m.SetReply(r)
		m.Rcode = dns.RcodeNameError
		w.WriteMsg(m)
		return
	}

	// Forward to upstream
	c := new(dns.Client)
	resp, _, err := c.Exchange(r, d.Upstream)
	if err != nil {
		slog.Error("upstream error", "component", "dns", "name", name, "err", err)
		dns.HandleFailed(w, r)
		return
	}
	w.WriteMsg(resp)
}
