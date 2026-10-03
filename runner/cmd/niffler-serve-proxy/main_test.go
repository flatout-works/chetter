package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func testBus(t *testing.T) *nats.Conn {
	t.Helper()
	bin, e := exec.LookPath("nats-server")
	if e != nil {
		t.Skip("nats-server required")
	}
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cmd := exec.Command(bin, "-a", "127.0.0.1", "-p", fmt.Sprint(port))
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	var nc *nats.Conn
	for i := 0; i < 100; i++ {
		nc, e = nats.Connect(fmt.Sprintf("nats://127.0.0.1:%d", port), nats.Timeout(100*time.Millisecond))
		if e == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(nc.Close)
	return nc
}
func reply(nc *nats.Conn, m *nats.Msg, args any) {
	raw, _ := json.Marshal(args)
	data, _ := json.Marshal(envelope{V: 1, ID: "response", Kind: "result", Args: raw})
	nc.Publish(m.Reply, data)
}
func testBridge(t *testing.T, mode string) *bridge {
	t.Helper()
	ws := t.TempDir()
	root := filepath.Join(ws, ".niffler", "runtime")
	os.MkdirAll(filepath.Join(root, "var"), 0700)
	os.WriteFile(filepath.Join(ws, ".niffler", "mcp.json"), []byte(`{"servers":[]}`), 0600)
	return &bridge{nc: testBus(t), workspace: ws, root: root, password: "secret", sessions: map[string]string{"sid": "sid"}, listeners: map[chan event]bool{}, command: func(_ string, args ...string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], append([]string{"-test.run=TestNativeHelper", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "NIFFLER_TEST_HELPER="+mode)
		return cmd
	}}
}

// Re-exec the test binary as a signal-aware native NDJSON driver. No shell,
// provider keys, paid requests, or coupling to Niffler implementation details.
func TestNativeHelper(t *testing.T) {
	mode := os.Getenv("NIFFLER_TEST_HELPER")
	if mode == "" {
		return
	}
	flags := map[string]string{}
	for _, arg := range os.Args {
		if k, v, ok := strings.Cut(strings.TrimPrefix(arg, "--"), "="); ok {
			flags[k] = v
		}
	}
	id := flags["session"]
	write := func(v any) { json.NewEncoder(os.Stdout).Encode(v) }
	if mode == "malformed" {
		fmt.Println("not-json")
		os.Exit(3)
	}
	if mode == "no-result" {
		write(map[string]any{"type": "error", "message": "startup failed"})
		os.Exit(3)
	}
	if mode == "cancel" {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)
		write(map[string]any{"type": "event", "subject": "ev.session." + id + ".token", "data": map[string]any{"sessionId": id, "content": "waiting"}})
		<-sig
	}
	if flags["mcp-file"] == "" || flags["root"] == "" || flags["bus"] == "" || flags["cwd"] == "" {
		write(map[string]any{"type": "error", "message": "missing native driver flags"})
		os.Exit(3)
	}
	export := flags["export"]
	body := `{"id":"sid:1","message":{"role":"user","content":"prompt"}}` + "\n" + `{"id":"sid:2","message":{"role":"assistant","content":"completed","tool_calls":[{"id":"call"}]}}` + "\n"
	os.WriteFile(export, []byte(body), 0600)
	outcome, detail := "success", ""
	if mode == "cancel" {
		outcome = "cancelled"
		detail = "cancelled"
	}
	if mode == "error" {
		outcome = "budget-exhausted"
		detail = "round budget exhausted"
	}
	usage := map[string]any{"turnId": "turn-1", "outcome": outcome, "promptTokens": 10, "completionTokens": 4, "cacheReadTokens": 3, "cacheWriteTokens": 2, "reasoningTokens": 1, "usageReported": true, "descendantsExcluded": true}
	write(map[string]any{"type": "event", "subject": "ev.session." + id + ".turn", "data": map[string]any{"sessionId": id, "turnId": "turn-1", "phase": "done", "outcome": outcome, "usage": usage}})
	result := map[string]any{"type": "result", "sessionId": id, "turnId": "turn-1", "outcome": outcome, "reply": "completed", "turnError": detail, "usage": usage}
	write(result)
	if mode == "duplicate" {
		write(result)
	}
	if outcome != "success" {
		os.Exit(1)
	}
	os.Exit(0)
}
func post(b *bridge, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer secret")
	b.handler().ServeHTTP(w, r)
	return w
}
func TestNativePromptAndTerminalFailure(t *testing.T) {
	for _, mode := range []string{"success", "error", "no-result", "malformed", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			b := testBridge(t, mode)
			w := post(b, "/session/sid/message", `{"prompt":"work","model":"model"}`)
			want := 200
			if mode != "success" {
				want = 502
			}
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
			if mode == "success" || mode == "error" {
				var out struct {
					TurnID string `json:"turnId"`
					Usage  struct {
						Prompt int `json:"promptTokens"`
					} `json:"usage"`
				}
				if e := json.Unmarshal(w.Body.Bytes(), &out); e != nil {
					t.Fatal(e)
				}
				if out.TurnID != "turn-1" || out.Usage.Prompt != 10 {
					t.Fatal(w.Body.String())
				}
				if _, e := os.Stat(filepath.Join(b.workspace, ".niffler", "exports", "sid.md")); e != nil {
					t.Fatal(e)
				}
			}
		})
	}
}
func TestAuthenticationAndUnknownSession(t *testing.T) {
	b := testBridge(t, "success")
	w := httptest.NewRecorder()
	b.handler().ServeHTTP(w, httptest.NewRequest("POST", "/session", nil))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w = post(b, "/session/unknown/message", `{"prompt":"work"}`); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
func TestNativeResumeAndMissingResume(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			b := testBridge(t, "success")
			_, _ = b.nc.Subscribe("svc.store.call", func(m *nats.Msg) { reply(b.nc, m, map[string]any{"ok": exists}) })
			b.nc.Flush()
			w := post(b, "/session/native/message", `{"prompt":"continue","resume_session_id":"native"}`)
			want := 404
			if exists {
				want = 200
			}
			if w.Code != want {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}
func TestCancellationWaitsForNativeExport(t *testing.T) {
	b := testBridge(t, "cancel")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- post(b, "/session/sid/message", `{"prompt":"work"}`) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		ready := len(b.history) > 0
		b.mu.Unlock()
		if ready {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, c := context.WithTimeout(context.Background(), 3*time.Second)
	defer c()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/session/sid/abort", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer secret")
	b.handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	response := <-done
	if response.Code != 502 || !strings.Contains(response.Body.String(), "cancelled") {
		t.Fatal(response.Code, response.Body.String())
	}
	if _, e := os.Stat(filepath.Join(b.workspace, ".niffler", "exports", "sid.md")); e != nil {
		t.Fatal(e)
	}
}
func TestTranscriptRendering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "export.jsonl")
	os.WriteFile(path, []byte(`{"id":"1","message":{"role":"assistant","content":"reply","reasoning":"reason","tool_calls":[{"id":"c"}]}}`+"\n"), 0600)
	text, e := renderTranscript(path)
	if e != nil || !strings.Contains(text, "reply") || !strings.Contains(text, "Thinking") || !strings.Contains(text, "```json") {
		t.Fatal(text, e)
	}
	os.WriteFile(path, []byte("bad\n"), 0600)
	if _, e = renderTranscript(path); e == nil {
		t.Fatal("malformed export accepted")
	}
}
