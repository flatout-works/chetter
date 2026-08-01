package network

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMCPRelayForwardsToTarget(t *testing.T) {
	var receivedAuth string
	var receivedBody string
	var receivedClaimID string
	var receivedTaskID string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Errorf("path = %q, want /mcp", r.URL.Path)
		}
		receivedAuth = r.Header.Get("Authorization")
		receivedClaimID = r.Header.Get("X-Chetter-Claim-ID")
		receivedTaskID = r.Header.Get("X-Chetter-Task-ID")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		receivedBody = string(body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()

	relay, err := NewMCPRelay("127.0.0.1:0", target.URL+"/mcp", "relay-token")
	if err != nil {
		t.Fatalf("new relay: %v", err)
	}
	unregister, err := relay.RegisterClaim("task-token", "task-1", "exec-1")
	if err != nil {
		t.Fatalf("register claim: %v", err)
	}
	defer unregister()
	if err := relay.Start(); err != nil {
		t.Fatalf("start relay: %v", err)
	}
	t.Cleanup(func() { _ = relay.Stop() })

	request, err := http.NewRequest(http.MethodPost, "http://"+relay.Addr()+"/mcp", strings.NewReader(`{"jsonrpc":"2.0"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer task-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("relay request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	if receivedAuth != "Bearer relay-token" {
		t.Errorf("authorization = %q", receivedAuth)
	}
	if receivedBody != `{"jsonrpc":"2.0"}` {
		t.Errorf("body = %q", receivedBody)
	}
	if receivedClaimID != "exec-1" || receivedTaskID != "task-1" {
		t.Errorf("claim headers = (%q, %q), want (exec-1, task-1)", receivedClaimID, receivedTaskID)
	}
}

func TestMCPRelayRejectsUnauthorizedRequestsWithoutContactingUpstream(t *testing.T) {
	var upstreamCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer target.Close()

	relay, err := NewMCPRelay("127.0.0.1:0", target.URL+"/mcp", "upstream-token")
	if err != nil {
		t.Fatal(err)
	}
	unregister, err := relay.RegisterClaim("active-token", "task-1", "exec-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := relay.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = relay.Stop() })

	for _, authorization := range []string{"", "Bearer wrong-token"} {
		req, err := http.NewRequest(http.MethodPost, "http://"+relay.Addr()+"/mcp", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", authorization)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Authorization %q status = %d, want 401", authorization, resp.StatusCode)
		}
	}

	unregister()
	req, err := http.NewRequest(http.MethodPost, "http://"+relay.Addr()+"/mcp", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer active-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unregistered token status = %d, want 401", resp.StatusCode)
	}
	if upstreamCalls.Load() != 0 {
		t.Fatalf("upstream received %d unauthorized requests", upstreamCalls.Load())
	}
	if relay.RejectedRequests() != 3 {
		t.Fatalf("rejected requests = %d, want 3", relay.RejectedRequests())
	}
}

func TestMCPRelayRejectsDuplicateOrIncompleteClaims(t *testing.T) {
	relay, err := NewMCPRelay("127.0.0.1:0", "http://127.0.0.1:8080/mcp", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay.RegisterClaim("", "task", "exec"); err == nil {
		t.Fatal("empty token was accepted")
	}
	unregister, err := relay.RegisterClaim("token", "task", "exec")
	if err != nil {
		t.Fatal(err)
	}
	defer unregister()
	if _, err := relay.RegisterClaim("token", "other-task", "other-exec"); err == nil {
		t.Fatal("duplicate token was accepted")
	}
}

func TestNewMCPRelayRejectsInvalidTarget(t *testing.T) {
	for _, target := range []string{"", "ftp://chetter-mcp:8080/mcp", "http:///mcp"} {
		t.Run(target, func(t *testing.T) {
			if _, err := NewMCPRelay(":0", target, ""); err == nil {
				t.Fatal("expected invalid target error")
			}
		})
	}
}
