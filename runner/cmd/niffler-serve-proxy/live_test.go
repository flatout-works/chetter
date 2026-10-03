package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flatout-works/chetter/runner/harness/niffler"
	"github.com/flatout-works/chetter/runner/internal/mcp"
	"github.com/flatout-works/chetter/runner/internal/task"
)

// Optional real-stack contract gate: no paid provider calls or host bus. The
// fake provider exercises MCP discovery/call, streaming, usage and continuation.
func TestLiveNiffler(t *testing.T) {
	home := os.Getenv("CHETTER_NIFFLER_TEST_HOME")
	if home == "" {
		t.Skip("set CHETTER_NIFFLER_TEST_HOME to a built Niffler distribution")
	}
	proxy := os.Getenv("CHETTER_NIFFLER_TEST_PROXY")
	if proxy == "" {
		t.Fatal("set CHETTER_NIFFLER_TEST_PROXY to built niffler-serve-proxy")
	}
	ws := t.TempDir()
	m, e := mcp.NewServer()
	if e != nil {
		t.Fatal(e)
	}
	defer m.Close()
	called := make(chan struct{}, 1)
	m.RegisterTool(mcp.ToolDef{Name: "test_echo", Description: "Echo test", InputSchema: map[string]any{"type": "object", "properties": map[string]any{}}}, func(ctx context.Context, a map[string]any) (any, error) {
		select {
		case called <- struct{}{}:
		default:
		}
		return map[string]any{"echo": "OK"}, nil
	})
	requests := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var in map[string]any
		if e := json.NewDecoder(r.Body).Decode(&in); e != nil {
			t.Error(e)
			return
		}
		if r.Header.Get("Authorization") != "Bearer fake-key" {
			t.Error("provider auth")
		}
		requests++
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		chunks := []any{}
		if requests == 1 {
			tools, _ := json.Marshal(in["tools"])
			if !strings.Contains(string(tools), "mcp_runner_bridge_test_echo") {
				t.Errorf("MCP tool absent before first turn: %s", tools)
			}
			chunks = append(chunks, map[string]any{"id": "c1", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call1", "type": "function", "function": map[string]any{"name": "mcp_runner_bridge_test_echo", "arguments": "{}"}}}}, "finish_reason": "tool_calls"}}})
		} else {
			chunks = append(chunks, map[string]any{"id": "c2", "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": "completed"}, "finish_reason": "stop"}}})
		}
		chunks = append(chunks, map[string]any{"id": "usage", "choices": []any{}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13, "prompt_tokens_details": map[string]any{"cached_tokens": 2, "cache_write_tokens": 1}, "completion_tokens_details": map[string]any{"reasoning_tokens": 1}}})
		for _, v := range chunks {
			raw, _ := json.Marshal(v)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			f.Flush()
		}
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		f.Flush()
	}))
	defer provider.Close()
	t.Setenv("LIVE_TEST_KEY", "fake-key")
	req := task.TaskRequest{Harness: "niffler", ProviderID: "fake", ModelID: "fake-model", ProviderBaseURL: provider.URL + "/v1", ProviderAPIKeyEnv: "LIVE_TEST_KEY", RunnerMCPToken: m.Token(), Prompt: "Use test_echo then finish", TimeoutSec: 60}
	h := niffler.New()
	if e = h.GenerateConfig(ws, "http://"+m.Addr()+"/mcp", "", "", req, true); e != nil {
		t.Fatal(e)
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	start := func() *exec.Cmd {
		cmd := exec.Command(proxy, "--port", fmt.Sprint(port))
		cmd.Dir = ws
		env := os.Environ()
		for k, v := range h.Env(ws, "secret", req) {
			env = append(env, k+"="+v)
		}
		cmd.Env = append(env, "CHETTER_NIFFLER_HOME="+home, "NIF_MODELS_AUTO_REFRESH=0", "NIF_REPOMAP_AUTOAPPEND=0")
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if e := cmd.Start(); e != nil {
			t.Fatal(e)
		}
		return cmd
	}
	cmd := start()
	defer func() {
		if cmd != nil {
			_ = cmd.Process.Signal(os.Interrupt)
			_ = cmd.Wait()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	if e = h.WaitForReady(ctx, base, "secret", 90*time.Second); e != nil {
		t.Fatal(e)
	}
	sid, e := h.CreateSession(ctx, base, "secret")
	if e != nil {
		t.Fatal(e)
	}
	s, e := h.SendPrompt(ctx, base, sid, "secret", req, ws, 60*time.Second)
	if e != nil || s != "completed" {
		t.Fatal(s, e)
	}
	usage := func() task.TokenUsage {
		r, e := http.NewRequestWithContext(ctx, http.MethodPost, base+"/session/"+sid+"/abort", strings.NewReader("{}"))
		if e != nil {
			t.Fatal(e)
		}
		r.Header.Set("Authorization", "Bearer secret")
		resp, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		var out struct {
			Usage niffler.TurnUsage `json:"usage"`
		}
		if e = json.NewDecoder(resp.Body).Decode(&out); e != nil || resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode, e)
		}
		return out.Usage.TaskUsage()
	}
	u := usage()
	// The OpenAI chat adapter does not expose cache-write usage; reasoning
	// does travel through it. Cache-write mapping is covered by native fixture
	// and adapter unit tests (Anthropic is the provider that reports it).
	if u.InputTokens != 20 || u.OutputTokens != 6 || u.CacheReadTokens != 4 || u.CacheWriteTokens != 0 || u.ReasoningTokens != 2 {
		t.Fatalf("fresh activation usage: %+v", u)
	}
	select {
	case <-called:
	default:
		t.Fatal("MCP tool not invoked")
	}
	text, e := h.ReadSessionExport(ws, sid)
	if e != nil || !strings.Contains(text, "completed") || !strings.Contains(text, "test_echo") {
		t.Fatal(text, e)
	}
	_ = cmd.Process.Signal(os.Interrupt)
	if e = cmd.Wait(); e != nil {
		t.Fatal(e)
	}
	cmd = nil
	// Restart the owned harness and continue its durable store, refreshing MCP
	// capability config. Chetter resumes directly with the native session ID.
	req.Prompt = "Continue the task"
	req.ResumeHarnessSessionID = sid
	h = niffler.New()
	cmd = start()
	if e = h.WaitForReady(ctx, base, "secret", 90*time.Second); e != nil {
		t.Fatal(e)
	}
	s, e = h.SendPrompt(ctx, base, sid, "secret", req, ws, 60*time.Second)
	if e != nil || s != "completed" {
		t.Fatal(s, e)
	}
	u = usage()
	if u.InputTokens != 10 || u.OutputTokens != 3 || u.CacheReadTokens != 2 || u.CacheWriteTokens != 0 || u.ReasoningTokens != 1 {
		t.Fatalf("resumed activation double-counted history: %+v", u)
	}
	text, e = h.ReadSessionExport(ws, sid)
	if e != nil || strings.Count(text, "## user") != 2 {
		t.Fatal(text, e)
	}
	if _, e = os.Stat(filepath.Join(ws, ".niffler", "runtime", "var", "store.db")); e != nil {
		t.Fatal(e)
	}
}
