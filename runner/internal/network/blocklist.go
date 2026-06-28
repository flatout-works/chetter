package network

import (
	"net/url"
	"strings"
)

func domainMatches(host string, domains []string) bool {
	host = normalizePolicyHost(host)
	for _, d := range domains {
		d = normalizePolicyHost(d)
		if d == "" {
			continue
		}
		if strings.EqualFold(host, d) || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func isBlocked(host string, blockedDomains []string) bool {
	return domainMatches(host, blockedDomains)
}

func normalizePolicyHost(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimSuffix(value, ".")
	if value == "" {
		return ""
	}
	if parsed, err := url.Parse("//" + value); err == nil && parsed.Hostname() != "" {
		return strings.ToLower(parsed.Hostname())
	}
	return strings.ToLower(strings.Trim(value, "[]"))
}
