package network

import (
	"log/slog"
	"net"
	"strings"
	"sync"

	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/miekg/dns"
)

// DNSProxy forwards allowed DNS queries to a platform resolver and suppresses
// AAAA responses to avoid stalls inside isolated containers.
type DNSProxy struct {
	ListenAddr     string
	Upstream       string
	AllowedDomains []string
	BlockedDomains []string
	mu             sync.Mutex
	servers        []*dns.Server
}

// NewDNSProxy creates a DNS proxy.
func NewDNSProxy(listenAddr, upstream string, allowed, blocked []string) *DNSProxy {
	if upstream == "" {
		upstream = config.DefaultDNSUpstream
	}
	return &DNSProxy{
		ListenAddr:     listenAddr,
		Upstream:       upstream,
		AllowedDomains: allowed,
		BlockedDomains: blocked,
	}
}

// Start begins serving TCP and UDP DNS requests.
func (d *DNSProxy) Start() error {
	mux := dns.NewServeMux()
	mux.HandleFunc(".", d.handleRequest)

	udpConn, err := net.ListenPacket("udp", d.ListenAddr)
	if err != nil {
		return err
	}
	tcpListener, err := net.Listen("tcp", d.ListenAddr)
	if err != nil {
		_ = udpConn.Close()
		return err
	}

	udpServer := &dns.Server{PacketConn: udpConn, Handler: mux}
	tcpServer := &dns.Server{Listener: tcpListener, Handler: mux}
	d.mu.Lock()
	d.servers = []*dns.Server{udpServer, tcpServer}
	d.mu.Unlock()
	slog.Info("starting", "component", "dns", "addr", d.ListenAddr, "upstream", d.Upstream)
	go d.serve("udp", udpServer)
	go d.serve("tcp", tcpServer)
	return nil
}

// Stop shuts down the DNS server.
func (d *DNSProxy) Stop() error {
	d.mu.Lock()
	servers := append([]*dns.Server(nil), d.servers...)
	d.servers = nil
	d.mu.Unlock()
	for _, server := range servers {
		if err := server.Shutdown(); err != nil {
			return err
		}
	}
	return nil
}

func (d *DNSProxy) serve(network string, server *dns.Server) {
	if err := server.ActivateAndServe(); err != nil {
		slog.Warn("dns server stopped", "network", network, "err", err)
	}
}

func (d *DNSProxy) handleRequest(w dns.ResponseWriter, r *dns.Msg) {
	if len(r.Question) == 0 {
		dns.HandleFailed(w, r)
		return
	}

	q := r.Question[0]
	name := strings.TrimSuffix(q.Name, ".")

	// Strip AAAA records to force IPv4-only and avoid Happy Eyeballs
	// stalls inside containers where IPv6 is un-routed. Return NOERROR with
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
	if len(d.AllowedDomains) > 0 && !domainMatches(name, d.AllowedDomains) {
		slog.Warn("NOT ALLOWED", "component", "dns", "name", name)
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
