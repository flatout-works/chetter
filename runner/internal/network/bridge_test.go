package network

import (
	"strings"
	"testing"
)

func TestNewTaskNetworkHandlesShortTaskID(t *testing.T) {
	tn := newTaskNetwork("a", 42)

	if tn.TaskID != "a" {
		t.Fatalf("TaskID = %q, want a", tn.TaskID)
	}
	for name, value := range map[string]string{
		"bridge":   tn.Bridge,
		"vethHost": tn.VethHost,
		"vethPeer": tn.VethPeer,
	} {
		if len(value) > 15 {
			t.Fatalf("%s name %q is longer than Linux IFNAMSIZ limit", name, value)
		}
	}
	if !strings.HasPrefix(tn.NetNSPath, "/run/netns/") {
		t.Fatalf("NetNSPath = %q, want /run/netns path", tn.NetNSPath)
	}
}

func TestAllocateTaskNetworkReturnsUniqueSubnets(t *testing.T) {
	bm := NewBridgeManager(":18080", ":5300")
	seen := make(map[string]bool)

	for i := 0; i < 20; i++ {
		tn, err := bm.allocateTaskNetwork(string(rune('a' + i)))
		if err != nil {
			t.Fatalf("allocate network %d: %v", i, err)
		}
		if seen[tn.Subnet] {
			t.Fatalf("duplicate subnet allocated: %s", tn.Subnet)
		}
		seen[tn.Subnet] = true
	}
}

func TestReleaseTaskNetworkAllowsReuse(t *testing.T) {
	bm := NewBridgeManager(":18080", ":5300")

	first, err := bm.allocateTaskNetwork("task")
	if err != nil {
		t.Fatalf("allocate first: %v", err)
	}
	bm.releaseTaskNetwork(first)

	second, err := bm.allocateTaskNetwork("task")
	if err != nil {
		t.Fatalf("allocate second: %v", err)
	}
	if first.Subnet != second.Subnet {
		t.Fatalf("subnet after release = %s, want %s", second.Subnet, first.Subnet)
	}
}

func TestProxyPort(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want string
	}{
		{addr: ":18080", want: "18080"},
		{addr: "127.0.0.1:18080", want: "18080"},
		{addr: "18080", want: "18080"},
	} {
		if got := proxyPort(tc.addr); got != tc.want {
			t.Fatalf("proxyPort(%q) = %q, want %q", tc.addr, got, tc.want)
		}
	}
}
