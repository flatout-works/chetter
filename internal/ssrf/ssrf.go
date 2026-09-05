// Package ssrf implements the SSRF-safe destination policy and HTTP client
// for outbound webhook delivery from the Chetter control plane.
//
// Event-callback webhook/slack actions (and later the unified inbound/outbound
// webhook platform tracked by epic #253) deliver to URLs taken from
// configuration. Nothing previously validated those destinations: a team token
// that can create/update a callback (or a compromised config path) could point
// the server at any host on the internal network or at cloud metadata
// endpoints, turning the control plane into an SSRF relay. This is finding S17
// in docs/plans/2026-07-23-001-quality-hardening-plan.md and issue #337.
//
// The policy is enforced in two layers:
//
//   - ValidateDestination rejects a disallowed URL statically at callback
//     create/update time (scheme, userinfo/fragment, literal-IP ranges, and
//     well-known metadata/local hostnames). Hostnames cannot be fully checked
//     without DNS, and DNS is mutable, so this layer is best-effort and never
//     the security boundary.
//   - The client returned by NewClient re-validates the request URL before any
//     network I/O and enforces the policy on every address actually dialed:
//     the hostname is resolved once, every resolved address is checked, and a
//     validated address is dialed directly without re-resolution, so a DNS
//     rebinding between validation and dial cannot reach a blocked address.
//
// See issue #337.
package ssrf

import (
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// Policy controls which destinations outbound webhook delivery may reach.
// Every field hardens by default; each override must be set explicitly by an
// operator (via server configuration) and is never derived from payloads or
// self-service callback config.
type Policy struct {
	// AllowHTTP permits http:// destinations in addition to https://.
	// Non-default and explicit: the SSRF-safe transport is HTTPS-only unless
	// an operator turns this on (e.g. loopback development mode).
	AllowHTTP bool

	// AllowPrivate permits loopback, link-local (including the cloud metadata
	// range 169.254.0.0/16), private, shared, multicast, and reserved ranges.
	// This is the documented escape hatch for trusted single-tenant
	// deployments and loopback development mode; the unified webhook platform
	// plan (epic #253) calls it the explicit operator policy.
	AllowPrivate bool

	// Allowlist holds operator-supplied exemptions: literal IPs or CIDRs
	// (e.g. "10.0.0.0/8", "192.168.1.5") and hostnames (e.g. "hooks.internal"
	// or ".internal.example", which also matches subdomains). Entries are
	// parsed on use; a malformed entry fails closed. Hostname entries are
	// trusted explicitly: their resolved addresses skip the range checks but
	// are still resolved once and dialed directly (no re-resolution window).
	Allowlist []string
}

// Error is a destination-policy rejection. Reason is a stable machine-readable
// category for audit/log classification; Detail is a human-readable
// explanation that never includes credentials, headers, or query strings.
type Error struct {
	Reason string
	Detail string
}

func (e *Error) Error() string {
	return "webhook destination rejected: " + e.Detail
}

// Validate reports whether the policy is well formed so a malformed allowlist
// fails closed at startup (config validation) instead of at delivery time.
func (p Policy) Validate() error {
	_, err := p.compile()
	return err
}

type compiledPolicy struct {
	allowHTTP     bool
	allowPrivate  bool
	allowPrefixes []netip.Prefix
	allowHosts    []string
}

func (p Policy) compile() (compiledPolicy, error) {
	cp := compiledPolicy{allowHTTP: p.AllowHTTP, allowPrivate: p.AllowPrivate}
	for _, raw := range p.Allowlist {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			return cp, fmt.Errorf("empty allowlist entry")
		}
		if strings.ContainsAny(entry, " \t\r\n") {
			return cp, fmt.Errorf("allowlist entry %q contains whitespace", raw)
		}
		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				return cp, fmt.Errorf("allowlist entry %q is not a valid CIDR: %v", raw, err)
			}
			cp.allowPrefixes = append(cp.allowPrefixes, prefix.Masked())
			continue
		}
		if addr, err := netip.ParseAddr(entry); err == nil {
			bits := 128
			if addr.Is4() {
				bits = 32
			}
			cp.allowPrefixes = append(cp.allowPrefixes, netip.PrefixFrom(addr, bits))
			continue
		}
		host := normalizeHost(entry)
		if !validHostnameEntry(host) {
			return cp, fmt.Errorf("allowlist entry %q is not a valid IP, CIDR, or hostname", raw)
		}
		cp.allowHosts = append(cp.allowHosts, host)
	}
	return cp, nil
}

// hostAllowed reports whether host is exactly or a subdomain of an allowlisted
// hostname entry.
func (cp compiledPolicy) hostAllowed(host string) bool {
	host = normalizeHost(host)
	for _, entry := range cp.allowHosts {
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

func (cp compiledPolicy) addrAllowed(addr netip.Addr) bool {
	for _, prefix := range cp.allowPrefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// blockedRanges holds ranges that netip's classifier helpers do not cover:
// documentation/TEST-NET, benchmarking, shared (CGNAT), 6to4/site-local
// remnants, and reserved space including the limited broadcast address.
var blockedRanges = []struct {
	prefix netip.Prefix
	class  string
	human  string
}{
	{prefix: mustPrefix("0.0.0.0/8"), class: "reserved", human: "a reserved address"},              // "this network"
	{prefix: mustPrefix("100.64.0.0/10"), class: "shared", human: "shared address space"},          // RFC 6598 CGNAT
	{prefix: mustPrefix("192.0.0.0/24"), class: "reserved", human: "a reserved address"},           // IETF protocol assignments
	{prefix: mustPrefix("192.0.2.0/24"), class: "documentation", human: "a documentation address"}, // TEST-NET-1
	{prefix: mustPrefix("198.18.0.0/15"), class: "benchmarking", human: "a benchmarking address"},
	{prefix: mustPrefix("198.51.100.0/24"), class: "documentation", human: "a documentation address"}, // TEST-NET-2
	{prefix: mustPrefix("203.0.113.0/24"), class: "documentation", human: "a documentation address"},  // TEST-NET-3
	{prefix: mustPrefix("240.0.0.0/4"), class: "reserved", human: "a reserved address"},               // incl. 255.255.255.255
	{prefix: mustPrefix("2001:db8::/32"), class: "documentation", human: "a documentation address"},
	{prefix: mustPrefix("2001:10::/28"), class: "reserved", human: "a reserved address"},  // ORCHID
	{prefix: mustPrefix("2002::/16"), class: "reserved", human: "a reserved address"},     // 6to4
	{prefix: mustPrefix("fec0::/10"), class: "site_local", human: "a site-local address"}, // deprecated
}

func mustPrefix(s string) netip.Prefix {
	p, err := netip.ParsePrefix(s)
	if err != nil {
		panic(fmt.Sprintf("ssrf: invalid built-in prefix %q: %v", s, err))
	}
	return p
}

// blockInfo classifies addr when it falls in a range outbound webhook delivery
// must never reach; it returns ("", "") for public addresses. The cloud
// metadata endpoints (169.254.169.254) live inside the link-local range.
func blockInfo(addr netip.Addr) (class, human string) {
	addr = addr.Unmap()
	switch {
	case addr.IsUnspecified():
		return "unspecified", "an unspecified address"
	case addr.IsLoopback():
		return "loopback", "a loopback address"
	case addr.IsMulticast():
		// Covers both 224.0.0.0/4 and ff00::/8; link-local multicast
		// (224.0.0.0/24, ff02::/16) is a subset and stays "multicast".
		return "multicast", "a multicast address"
	case addr.IsLinkLocalUnicast():
		return "link_local", "a link-local address"
	case addr.IsPrivate():
		// RFC 1918 (10/8, 172.16/12, 192.168/16) and RFC 4193 (fc00::/7).
		return "private_network", "a private network address"
	}
	for _, r := range blockedRanges {
		if r.prefix.Contains(addr) {
			return r.class, r.human
		}
	}
	return "", ""
}

// checkAddr returns nil when addr may be dialed under cp.
func checkAddr(addr netip.Addr, cp compiledPolicy) *Error {
	if cp.allowPrivate || cp.addrAllowed(addr) {
		return nil
	}
	if class, human := blockInfo(addr); class != "" {
		return &Error{
			Reason: class,
			Detail: fmt.Sprintf("destination address %s is %s", addr, human),
		}
	}
	return nil
}

// blockedHostClass returns a stable class when host is a well-known hostname
// that must never be a webhook destination. These are checked statically (no
// DNS) so configuration validation can reject them up front; the authoritative
// enforcement for hostnames is the per-address dial check.
func blockedHostClass(host string) string {
	host = normalizeHost(host)
	switch {
	case host == "localhost" || strings.HasSuffix(host, ".localhost"):
		return "localhost"
	case strings.HasSuffix(host, ".local"):
		// RFC 6762 mDNS names are link-local by definition.
		return "mdns"
	case host == "metadata.google.internal" || strings.HasSuffix(host, ".metadata.google.internal"):
		return "metadata"
	}
	return ""
}

// ValidateDestination applies the destination policy to a configured URL
// without resolving it: scheme, userinfo/fragment, literal-IP ranges, and
// well-known blocked hostnames. Hostnames are resolved and enforced per
// address by the client at dial time (see NewClient) so DNS rebinding cannot
// bypass the policy.
func ValidateDestination(rawURL string, pol Policy) error {
	cp, err := pol.compile()
	if err != nil {
		return &Error{Reason: "policy_invalid", Detail: err.Error()}
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return &Error{Reason: "invalid_url", Detail: fmt.Sprintf("cannot parse destination URL: %v", err)}
	}
	if cerr := checkURL(u, cp); cerr != nil {
		return cerr
	}
	return nil
}

func checkURL(u *url.URL, cp compiledPolicy) *Error {
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && cp.allowHTTP:
	case u.Scheme == "http":
		return &Error{Reason: "scheme", Detail: `destination scheme "http" is not allowed; https is required (set CHETTER_WEBHOOK_ALLOW_HTTP for an explicit non-default override)`}
	default:
		return &Error{Reason: "scheme", Detail: fmt.Sprintf("destination scheme %q is not allowed; only https is supported", u.Scheme)}
	}
	if u.Host == "" {
		return &Error{Reason: "invalid_url", Detail: "destination URL has no host"}
	}
	if u.User != nil {
		return &Error{Reason: "credentials", Detail: "destination URL must not contain userinfo (user:password@)"}
	}
	if u.Fragment != "" {
		return &Error{Reason: "fragment", Detail: "destination URL must not contain a fragment"}
	}
	host := normalizeHost(u.Hostname())
	if host == "" {
		return &Error{Reason: "invalid_url", Detail: "destination URL has no hostname"}
	}
	if !cp.hostAllowed(host) {
		if class := blockedHostClass(host); class != "" {
			return &Error{Reason: class, Detail: fmt.Sprintf("destination host %q is not allowed (well-known %s hostname)", u.Hostname(), class)}
		}
	}
	if ip, ok := parseIPLiteral(host); ok {
		if err := checkAddr(ip, cp); err != nil {
			return err
		}
	}
	return nil
}

// normalizeHost lowercases a host, drops an optional IPv6 zone, and trims one
// leading and one trailing dot for suffix matching.
func normalizeHost(host string) string {
	host = strings.ToLower(strings.TrimSpace(host))
	host = strings.TrimSuffix(host, ".")
	host = strings.TrimPrefix(host, ".")
	return host
}

func validHostnameEntry(host string) bool {
	if host == "" || len(host) > 253 {
		return false
	}
	for _, r := range host {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-':
		default:
			return false
		}
	}
	return true
}

// parseIPLiteral parses host as an IP literal, stripping any IPv6 zone
// (zones only occur on link-local addresses, which the policy blocks anyway).
func parseIPLiteral(host string) (netip.Addr, bool) {
	if i := strings.LastIndex(host, "%"); i >= 0 {
		host = host[:i]
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr, true
}
