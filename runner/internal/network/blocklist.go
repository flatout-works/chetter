package network

import "strings"

func domainMatches(host string, domains []string) bool {
	for _, d := range domains {
		if strings.EqualFold(host, d) || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

func isBlocked(host string, blockedDomains []string) bool {
	return domainMatches(host, blockedDomains)
}
