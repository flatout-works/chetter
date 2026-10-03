package niffler

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func testRequest() task.TaskRequest {
	return task.TaskRequest{Harness: "niffler", ProviderID: "deepseek", ModelID: "model", ProviderBaseURL: "https://api.deepseek.com", ProviderAPIKeyEnv: "TEST_NIFFLER_KEY"}
}
func archiveSkill(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	g := gzip.NewWriter(&b)
	w := tar.NewWriter(g)
	s := "---\nname: example\ndescription: A test skill\n---\nUseful instructions\n"
	if e := w.WriteHeader(&tar.Header{Name: "SKILL.md", Mode: 0644, Size: int64(len(s))}); e != nil {
		t.Fatal(e)
	}
	_, _ = w.Write([]byte(s))
	_ = w.Close()
	_ = g.Close()
	return b.Bytes()
}
func TestConfigCredentialsSkillsAndEnv(t *testing.T) {
	ws := t.TempDir()
	r := testRequest()
	r.RunnerMCPToken = "claim-token"
	r.AgentDefinition = "Task persona"
	r.SkillDefinitions = map[string][]byte{"example": archiveSkill(t)}
	r.McpEndpoints = []task.MCPEndpoint{{Name: "context", URL: "https://example.test/mcp", BearerTokenEnv: "ENDPOINT_KEY"}}
	n := New()
	if e := n.GenerateConfig(ws, "http://runner/mcp", "http://relay/mcp", "relay-token", r, false); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(ws, ".niffler", "mcp.json"))
	if e != nil {
		t.Fatal(e)
	}
	var cfg Config
	if e = json.Unmarshal(raw, &cfg); e != nil {
		t.Fatal(e)
	}
	if len(cfg.Servers) != 3 {
		t.Fatalf("servers: %s", raw)
	}
	h := cfg.Servers[0]["headers"].(map[string]any)
	if h["Authorization"] != "Bearer claim-token" {
		t.Fatalf("headers: %v", h)
	}
	h = cfg.Servers[2]["headers"].(map[string]any)
	if h["Authorization"] != "Bearer ${ENDPOINT_KEY}" {
		t.Fatalf("endpoint indirection: %v", h)
	}
	st, _ := os.Stat(filepath.Join(ws, ".niffler", "mcp.json"))
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
	body, _ := os.ReadFile(filepath.Join(ws, ".niffler", "skills", "example", "SKILL.md"))
	if !bytes.Contains(body, []byte("Useful instructions")) {
		t.Fatal("archive not extracted")
	}
	t.Setenv("TEST_NIFFLER_KEY", "provider-secret")
	env := n.Env(ws, "proxy-token", r)
	var p map[string]map[string]any
	if e = json.Unmarshal([]byte(env["NIF_LLM_PROVIDERS"]), &p); e != nil {
		t.Fatal(e)
	}
	if p["chetter"]["apiKey"] != "provider-secret" || p["chetter"]["baseUrl"] != "https://api.deepseek.com/v1" {
		t.Fatal(p)
	}
	if env["NIF_ROOT"] != filepath.Join(ws, ".niffler", "runtime") {
		t.Fatal(env)
	}
}
func TestRefusesConfigSymlinksAndUnsupportedProvider(t *testing.T) {
	for _, leaf := range []string{".niffler", ".niffler/mcp.json", ".niffler/skills"} {
		t.Run(leaf, func(t *testing.T) {
			ws := t.TempDir()
			target := t.TempDir()
			if leaf != ".niffler" {
				_ = os.MkdirAll(filepath.Dir(filepath.Join(ws, leaf)), 0700)
			}
			if e := os.Symlink(target, filepath.Join(ws, leaf)); e != nil {
				t.Fatal(e)
			}
			if e := New().GenerateConfig(ws, "", "", "", testRequest(), true); e == nil {
				t.Fatal("symlink accepted")
			}
		})
	}
	r := testRequest()
	r.ProviderAPI = "bedrock"
	if e := New().GenerateConfig(t.TempDir(), "", "", "", r, true); e == nil {
		t.Fatal("unsupported API accepted")
	}
}
func TestHTTPPromptAndUsageReconciliation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("auth missing")
		}
		switch r.URL.Path {
		case "/session":
			_, _ = w.Write([]byte(`{"session_id":"sid"}`))
		case "/session/sid/message":
			var p map[string]any
			_ = json.NewDecoder(r.Body).Decode(&p)
			if p["resume_session_id"] != "old" || p["model"] != "model" {
				t.Error(p)
			}
			_, _ = w.Write([]byte(`{"summary":"done","turnId":"turn-1","outcome":"success","usage":{"promptTokens":10,"completionTokens":4,"cacheWriteTokens":2,"reasoningTokens":1}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	n := New()
	var got task.TokenUsage
	n.tokens = func(u task.TokenUsage) { got.InputTokens += u.InputTokens; got.OutputTokens += u.OutputTokens }
	n.account("turn-1", task.TokenUsage{InputTokens: 10, OutputTokens: 4, CacheWriteTokens: 2, ReasoningTokens: 1})
	r := testRequest()
	r.Prompt = "Work"
	r.ResumeHarnessSessionID = "old"
	sid, e := n.CreateSession(context.Background(), srv.URL, "secret")
	if e != nil {
		t.Fatal(e)
	}
	s, e := n.SendPrompt(context.Background(), srv.URL, sid, "secret", r, "", time.Second)
	if e != nil || s != "done" {
		t.Fatal(s, e)
	}
	n.account("turn-1", task.TokenUsage{InputTokens: 10, OutputTokens: 4, CacheWriteTokens: 2, ReasoningTokens: 1})
	if got.InputTokens != 10 || got.OutputTokens != 4 {
		t.Fatal(got)
	}
}
