package network

import (
	"fmt"
	"net"
)

// NarrowListenAddr replaces an unspecified listen host with host while
// preserving the configured port. Explicit non-wildcard hosts are retained.
func NarrowListenAddr(listenAddr, host string) (string, error) {
	configuredHost, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", fmt.Errorf("parse listen address %q: %w", listenAddr, err)
	}
	if port == "" {
		return "", fmt.Errorf("listen address %q has no port", listenAddr)
	}
	if configuredHost == "" || configuredHost == "0.0.0.0" || configuredHost == "::" {
		if net.ParseIP(host) == nil {
			return "", fmt.Errorf("replacement listen host %q is not an IP address", host)
		}
		configuredHost = host
	}
	return net.JoinHostPort(configuredHost, port), nil
}
