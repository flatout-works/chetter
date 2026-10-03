package controller

import (
	"context"
	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/harness/niffler"
	"github.com/flatout-works/chetter/runner/internal/config"
	"github.com/flatout-works/chetter/runner/internal/task"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSelectNifflerServeHarness(t *testing.T) {
	h := selectHarnessByName("niffler")
	if h.Name() != "niffler" {
		t.Fatal(h.Name())
	}
	s, ok := h.(harness.ServeHarness)
	if !ok {
		t.Fatal("Niffler must use shared serve lifecycle")
	}
	if got := s.ServeCommand(9000); len(got) != 3 || got[0] != "niffler-serve-proxy" {
		t.Fatal(got)
	}
}
func TestNifflerLocalRelayRetainsUpstreamCredential(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer upstream-secret" {
			t.Error("upstream auth missing")
		}
		w.WriteHeader(200)
	}))
	defer upstream.Close()
	cfg := &config.Config{}
	cfg.Execution.Backend = "local"
	cfg.ChetterMCP.URL = upstream.URL
	cfg.ChetterMCP.AuthToken = "upstream-secret"
	r := &Runner{cfg: cfg}
	req := task.TaskRequest{TaskID: "task", ExecutionID: "exec", RunnerMCPToken: "claim-secret"}
	url, token, close, e := r.nifflerLocalRelay(req, niffler.New())
	if e != nil {
		t.Fatal(e)
	}
	defer close()
	if token != "claim-secret" || url == upstream.URL {
		t.Fatal(url, token)
	}
	request, _ := http.NewRequestWithContext(context.Background(), "POST", url, nil)
	request.Header.Set("Authorization", "Bearer "+token)
	resp, e := http.DefaultClient.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatal(resp.StatusCode)
	}
}
