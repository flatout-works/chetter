package network

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/flatout-works/devfleet/runner/internal/executil"
)

// BridgeManager allocates per-task Linux bridges, network namespaces, and
// veth pairs, applying iptables rules for strict egress filtering.
type BridgeManager struct {
	ProxyAddr string // e.g. ":18080"
	DNSAddr   string // e.g. ":5300"

	mu        sync.Mutex
	allocated map[int]string
}

// NewBridgeManager creates a bridge manager.
func NewBridgeManager(proxyAddr, dnsAddr string) *BridgeManager {
	return &BridgeManager{ProxyAddr: proxyAddr, DNSAddr: dnsAddr, allocated: make(map[int]string)}
}

// TaskNetwork represents the network for one task.
type TaskNetwork struct {
	TaskID    string
	Bridge    string
	Subnet    string
	GatewayIP string
	GuestIP   string
	VethHost  string
	VethPeer  string
	NetNS     string
	NetNSPath string
	index     int
}

// Setup creates a per-task bridge, netns, veth pair, route, and policy rules.
func (bm *BridgeManager) Setup(ctx context.Context, taskID string) (*TaskNetwork, error) {
	tn, err := bm.allocateTaskNetwork(taskID)
	if err != nil {
		return nil, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = bm.Teardown(ctx, tn)
		}
	}()

	slog.Info("creating bridge and netns", "component", "net", "taskID", taskID, "bridge", tn.Bridge, "netns", tn.NetNS)

	// Enable bridge netfilter so iptables sees packets on bridge members
	_, _ = executil.Run(ctx, "modprobe", "br_netfilter")
	_, _ = executil.Run(ctx, "sysctl", "-w", "net.bridge.bridge-nf-call-iptables=1")
	_, _ = executil.Run(ctx, "sysctl", "-w", "net.bridge.bridge-nf-call-ip6tables=1")

	// Clean up any stale netns/bridge from a previous crashed run
	_, _ = executil.Run(ctx, "ip", "link", "delete", tn.Bridge)
	_, _ = executil.Run(ctx, "ip", "netns", "delete", tn.NetNS)
	_, _ = executil.Run(ctx, "ip", "link", "delete", tn.VethHost)

	if out, err := executil.Run(ctx, "ip", "netns", "add", tn.NetNS); err != nil {
		return nil, fmt.Errorf("add netns: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "ip", "link", "add", tn.Bridge, "type", "bridge"); err != nil {
		return nil, fmt.Errorf("add bridge: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "ip", "addr", "add", tn.GatewayIP+"/24", "dev", tn.Bridge); err != nil {
		return nil, fmt.Errorf("addr bridge: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "ip", "link", "set", tn.Bridge, "up"); err != nil {
		return nil, fmt.Errorf("up bridge: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "ip", "link", "add", tn.VethHost, "type", "veth", "peer", "name", tn.VethPeer); err != nil {
		return nil, fmt.Errorf("add veth: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "ip", "link", "set", tn.VethHost, "master", tn.Bridge); err != nil {
		return nil, fmt.Errorf("master veth: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "ip", "link", "set", tn.VethHost, "up"); err != nil {
		return nil, fmt.Errorf("up veth host: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "ip", "link", "set", tn.VethPeer, "netns", tn.NetNS); err != nil {
		return nil, fmt.Errorf("move veth peer: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "ip", "netns", "exec", tn.NetNS, "ip", "link", "set", "lo", "up"); err != nil {
		return nil, fmt.Errorf("up loopback: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "ip", "netns", "exec", tn.NetNS, "ip", "link", "set", tn.VethPeer, "name", "eth0"); err != nil {
		return nil, fmt.Errorf("rename veth peer: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "ip", "netns", "exec", tn.NetNS, "ip", "addr", "add", tn.GuestIP+"/24", "dev", "eth0"); err != nil {
		return nil, fmt.Errorf("addr veth peer: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "ip", "netns", "exec", tn.NetNS, "ip", "link", "set", "eth0", "up"); err != nil {
		return nil, fmt.Errorf("up veth peer: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "ip", "netns", "exec", tn.NetNS, "ip", "route", "add", "default", "via", tn.GatewayIP); err != nil {
		return nil, fmt.Errorf("default route: %w (%s)", err, out)
	}
	if err := bm.applyIPTables(ctx, tn); err != nil {
		return nil, fmt.Errorf("iptables: %w", err)
	}

	cleanup = false
	return tn, nil
}

// Teardown removes bridge, netns, veth, iptables rules, and subnet allocation.
func (bm *BridgeManager) Teardown(ctx context.Context, tn *TaskNetwork) error {
	if tn == nil {
		return nil
	}
	slog.Info("tearing down bridge and netns", "component", "net", "taskID", tn.TaskID, "bridge", tn.Bridge, "netns", tn.NetNS)
	_ = bm.removeIPTables(ctx, tn)
	_ = executil.RunIgnore(ctx, "ip", "link", "del", tn.VethHost)
	_ = executil.RunIgnore(ctx, "ip", "netns", "del", tn.NetNS)
	_ = executil.RunIgnore(ctx, "ip", "link", "del", tn.Bridge)
	bm.releaseTaskNetwork(tn)
	return nil
}

func (bm *BridgeManager) allocateTaskNetwork(taskID string) (*TaskNetwork, error) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	start := int(taskHash(taskID)[0])
	for attempt := 0; attempt < 200; attempt++ {
		index := 10 + ((start + attempt) % 200)
		if _, exists := bm.allocated[index]; exists {
			continue
		}
		bm.allocated[index] = taskID
		return newTaskNetwork(taskID, index), nil
	}
	return nil, fmt.Errorf("no task network subnets available")
}

func (bm *BridgeManager) releaseTaskNetwork(tn *TaskNetwork) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	if bm.allocated[tn.index] == tn.TaskID {
		delete(bm.allocated, tn.index)
	}
}

func newTaskNetwork(taskID string, index int) *TaskNetwork {
	suffix := taskSuffix(taskID)
	return &TaskNetwork{
		TaskID:    taskID,
		Bridge:    "br-" + suffix,
		Subnet:    fmt.Sprintf("10.200.%d.0/24", index),
		GatewayIP: fmt.Sprintf("10.200.%d.1", index),
		GuestIP:   fmt.Sprintf("10.200.%d.2", index),
		VethHost:  "vh-" + suffix,
		VethPeer:  "vp-" + suffix,
		NetNS:     "fo-" + suffix,
		NetNSPath: "/run/netns/fo-" + suffix,
		index:     index,
	}
}

func taskSuffix(taskID string) string {
	hash := taskHash(taskID)
	return hex.EncodeToString(hash[:4])
}

func taskHash(taskID string) [sha1.Size]byte {
	return sha1.Sum([]byte(taskID))
}

func (bm *BridgeManager) applyIPTables(ctx context.Context, tn *TaskNetwork) error {
	pPort := proxyPort(bm.ProxyAddr)
	if out, err := executil.Run(ctx, "iptables", "-I", "FORWARD", "1", "-s", tn.Subnet, "-d", "169.254.169.254/32", "-j", "REJECT"); err != nil {
		return fmt.Errorf("block metadata: %w (%s)", err, out)
	}
	dPort := proxyPort(bm.DNSAddr)
	for _, rule := range [][]string{
		{"-p", "tcp", "--dport", pPort},
		{"-p", "udp", "--dport", dPort},
		{"-p", "tcp", "--dport", dPort},
	} {
		args := append([]string{"-I", "INPUT", "1", "-s", tn.Subnet, "-d", tn.GatewayIP}, rule...)
		args = append(args, "-j", "ACCEPT")
		if out, err := executil.Run(ctx, "iptables", args...); err != nil {			return fmt.Errorf("allow host service input: %w (%s)", err, out)
		}
	}
	for _, proto := range []string{"udp", "tcp"} {
		if out, err := executil.Run(ctx, "iptables", "-t", "nat", "-A", "PREROUTING", "-s", tn.Subnet, "-p", proto, "--dport", "53", "-j", "REDIRECT", "--to-port", dPort); err != nil {
			return fmt.Errorf("redirect dns %s: %w (%s)", proto, err, out)
		}
	}
	if out, err := executil.Run(ctx, "iptables", "-A", "FORWARD", "-s", tn.Subnet, "-p", "tcp", "-m", "multiport", "--dports", "80,443", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("forward web egress: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "iptables", "-t", "nat", "-A", "POSTROUTING", "-s", tn.Subnet, "!", "-d", tn.Subnet, "-j", "MASQUERADE"); err != nil {
		return fmt.Errorf("masquerade egress: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "iptables", "-A", "FORWARD", "-d", tn.Subnet, "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT"); err != nil {
		return fmt.Errorf("forward established: %w (%s)", err, out)
	}
	if out, err := executil.Run(ctx, "iptables", "-A", "FORWARD", "-s", tn.Subnet, "-j", "REJECT"); err != nil {
		return fmt.Errorf("forward reject: %w (%s)", err, out)
	}
	slog.Info("iptables applied", "component", "net", "taskID", tn.TaskID, "subnet", tn.Subnet, "proxyPort", pPort, "dnsPort", dPort)
	return nil
}

func (bm *BridgeManager) removeIPTables(ctx context.Context, tn *TaskNetwork) error {
	pPort := proxyPort(bm.ProxyAddr)
	_ = executil.RunIgnore(ctx, "iptables", "-D", "FORWARD", "-s", tn.Subnet, "-d", "169.254.169.254/32", "-j", "REJECT")
	dPort := proxyPort(bm.DNSAddr)
	for _, rule := range [][]string{
		{"-p", "tcp", "--dport", pPort},
		{"-p", "udp", "--dport", dPort},
		{"-p", "tcp", "--dport", dPort},
	} {
		args := append([]string{"-D", "INPUT", "-s", tn.Subnet, "-d", tn.GatewayIP}, rule...)
		args = append(args, "-j", "ACCEPT")
		_ = executil.RunIgnore(ctx, "iptables", args...)
	}
	for _, proto := range []string{"udp", "tcp"} {
		_ = executil.RunIgnore(ctx, "iptables", "-t", "nat", "-D", "PREROUTING", "-s", tn.Subnet, "-p", proto, "--dport", "53", "-j", "REDIRECT", "--to-port", dPort)
	}
	_ = executil.RunIgnore(ctx, "iptables", "-D", "FORWARD", "-s", tn.Subnet, "-p", "tcp", "-m", "multiport", "--dports", "80,443", "-j", "ACCEPT")
	_ = executil.RunIgnore(ctx, "iptables", "-t", "nat", "-D", "POSTROUTING", "-s", tn.Subnet, "!", "-d", tn.Subnet, "-j", "MASQUERADE")
	_ = executil.RunIgnore(ctx, "iptables", "-D", "FORWARD", "-d", tn.Subnet, "-m", "state", "--state", "ESTABLISHED,RELATED", "-j", "ACCEPT")
	_ = executil.RunIgnore(ctx, "iptables", "-D", "FORWARD", "-s", tn.Subnet, "-j", "REJECT")
	return nil
}

func proxyPort(proxyAddr string) string {
	if idx := strings.LastIndex(proxyAddr, ":"); idx != -1 {
		return proxyAddr[idx+1:]
	}
	return proxyAddr
}

// EnableIPForwarding enables net.ipv4.ip_forward.
func EnableIPForwarding() error {
	_, err := executil.Run(context.TODO(), "sysctl", "-w", "net.ipv4.ip_forward=1")
	return err
}
