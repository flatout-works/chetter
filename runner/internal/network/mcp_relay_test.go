package network

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMCPRelayForwardsToTarget(t *testing.T) {
	var receivedAuth string
	var receivedBody string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Errorf("path = %q, want /mcp", r.URL.Path)
		}
		receivedAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		receivedBody = string(body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer target.Close()

	relay, err := NewMCPRelay("127.0.0.1:0", target.URL+"/mcp")
	if err != nil {
		t.Fatalf("new relay: %v", err)
	}
	if err := relay.Start(); err != nil {
		t.Fatalf("start relay: %v", err)
	}
	t.Cleanup(func() { _ = relay.Stop() })

	request, err := http.NewRequest(http.MethodPost, "http://"+relay.Addr()+"/mcp", strings.NewReader(`{"jsonrpc":"2.0"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("relay request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusAccepted)
	}
	if receivedAuth != "Bearer test-token" {
		t.Errorf("authorization = %q", receivedAuth)
	}
	if receivedBody != `{"jsonrpc":"2.0"}` {
		t.Errorf("body = %q", receivedBody)
	}
}

func TestNewMCPRelayRejectsInvalidTarget(t *testing.T) {
	for _, target := range []string{"", "ftp://chetter-mcp:8080/mcp", "http:///mcp"} {
		t.Run(target, func(t *testing.T) {
			if _, err := NewMCPRelay(":0", target); err == nil {
				t.Fatal("expected invalid target error")
			}
		})
	}
}
