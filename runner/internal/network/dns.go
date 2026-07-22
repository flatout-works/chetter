package network

import (
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"

	"github.com/miekg/dns"
)

// DNSProxy forwards allowed DNS queries to a platform resolver and suppresses
// AAAA responses to avoid stalls inside isolated containers.
type DNSProxy struct {
	ListenAddr     string
	Upstream       string
	AllowedDomains []string
	BlockedDomains []string
	StaticRecords  map[string][]net.IP
	mu             sync.Mutex
	servers        []*dns.Server
}

// NewDNSProxy creates a DNS proxy.
func NewDNSProxy(listenAddr, upstream string, allowed, blocked []string, staticRecords map[string][]net.IP) *DNSProxy {
	records := make(map[string][]net.IP, len(staticRecords))
	for name, ips := range staticRecords {
		records[strings.ToLower(strings.TrimSuffix(name, "."))] = append([]net.IP(nil), ips...)
	}
	return &DNSProxy{
		ListenAddr:     listenAddr,
		Upstream:       upstream,
		AllowedDomains: allowed,
		BlockedDomains: blocked,
		StaticRecords:  records,
	}
}

// Start begins serving TCP and UDP DNS requests.
func (d *DNSProxy) Start() error {
	if d.Upstream == "" {
		return fmt.Errorf("DNS upstream is required")
	}

	mux := dns.NewServeMux()
	mux.HandleFunc(".", d.handleRequest)

	udpConn, err := net.ListenPacket("udp", d.ListenAddr)
	if err != nil {
		return err
	}
	tcpListener, err := net.Listen("tcp", samePortTCPAddr(udpConn.LocalAddr()))
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

// Addr returns the address shared by the proxy's UDP and TCP listeners.
func (d *DNSProxy) Addr() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.servers) == 0 || d.servers[0].PacketConn == nil {
		return ""
	}
	return d.servers[0].PacketConn.LocalAddr().String()
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

func samePortTCPAddr(addr net.Addr) string {
	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		return addr.String()
	}
	host := ""
	if udpAddr.IP != nil && !udpAddr.IP.IsUnspecified() {
		host = udpAddr.IP.String()
	}
	return net.JoinHostPort(host, strconv.Itoa(udpAddr.Port))
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

	if q.Qtype == dns.TypeA {
		if ips := d.StaticRecords[strings.ToLower(name)]; len(ips) > 0 {
			m := new(dns.Msg)
			m.SetReply(r)
			for _, ip := range ips {
				if v4 := ip.To4(); v4 != nil {
					m.Answer = append(m.Answer, &dns.A{
						Hdr: dns.RR_Header{Name: q.Name, Rrtype: dns.TypeA, Class: dns.ClassINET, Ttl: 60},
						A:   v4,
					})
				}
			}
			if len(m.Answer) > 0 {
				w.WriteMsg(m)
				return
			}
		}
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
