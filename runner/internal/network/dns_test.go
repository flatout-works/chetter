package network

import "testing"

func TestDNSPolicyMatching(t *testing.T) {
	d := NewDNSProxy(":0", "8.8.8.8:53", []string{"metadata.google.internal", "blocked.test"})

	for _, name := range []string{"metadata.google.internal", "a.metadata.google.internal", "blocked.test", "sub.blocked.test"} {
		if !isBlocked(name, d.BlockedDomains) {
			t.Fatalf("name %q should be blocked", name)
		}
	}
	if isBlocked("github.com", d.BlockedDomains) {
		t.Fatal("github.com should not be blocked")
	}
}
