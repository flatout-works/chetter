// Package niffler adapts Chetter's serve lifecycle to Niffler's native driver.
package niffler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/flatout-works/chetter/runner/harness"
	"github.com/flatout-works/chetter/runner/harness/mcpconfig"
	"github.com/flatout-works/chetter/runner/harness/transport"
	"github.com/flatout-works/chetter/runner/internal/skilltar"
	"github.com/flatout-works/chetter/runner/internal/task"
)

// TurnUsage mirrors docs/WIRE.md "Turn usage". Optional counters preserve
// absence; Chetter's numeric task rollup receives only provider-reported values.
type TurnUsage struct {
	TurnID           string `json:"turnId"`
	Outcome          string `json:"outcome"`
	PromptTokens     *int64 `json:"promptTokens,omitempty"`
	CompletionTokens *int64 `json:"completionTokens,omitempty"`
	CacheReadTokens  *int64 `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens *int64 `json:"cacheWriteTokens,omitempty"`
	ReasoningTokens  *int64 `json:"reasoningTokens,omitempty"`
}

func (u TurnUsage) TaskUsage() task.TokenUsage {
	value := func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	}
	return task.TokenUsage{InputTokens: value(u.PromptTokens), OutputTokens: value(u.CompletionTokens), CacheReadTokens: value(u.CacheReadTokens), CacheWriteTokens: value(u.CacheWriteTokens), ReasoningTokens: value(u.ReasoningTokens)}
}

type Niffler struct {
	mu        sync.Mutex
	usage     task.TokenUsage
	tokens    func(task.TokenUsage)
	accounted map[string]bool
}

var _ harness.ServeHarness = (*Niffler)(nil)

func New() *Niffler           { return &Niffler{accounted: map[string]bool{}} }
func (*Niffler) Name() string { return "niffler" }
func (*Niffler) ResolvedModelID(req task.TaskRequest) string {
	return req.ProviderID + "/" + req.ModelID
}
func (*Niffler) ServerPassword() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func (*Niffler) ServeCommand(port int) []string {
	return []string{"niffler-serve-proxy", "--port", strconv.Itoa(port)}
}
func (*Niffler) PipeOutput(id, stream string, r io.Reader) {
	harness.LogOutput("niffler", id, stream, r)
}

// Config contains only task-scoped capabilities; provider keys remain in env.
type Config struct {
	Servers []map[string]any `json:"servers"`
}

func (*Niffler) GenerateConfig(ws, runnerURL, chetterURL, chetterToken string, req task.TaskRequest, _ bool) error {
	if api := req.ProviderAPI; api != "" && api != "openai-completions" && api != "anthropic-messages" {
		return fmt.Errorf("niffler: unsupported provider API %q", api)
	}
	if req.ModelID == "" || req.ProviderBaseURL == "" || req.ProviderAPIKeyEnv == "" {
		return fmt.Errorf("niffler requires a resolved model, provider base URL and API key environment")
	}
	dir := filepath.Join(ws, ".niffler")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e := realDirectory(dir); e != nil {
		return e
	}
	// Never let a fresh checkout choose binaries, manifest or stored MCP state.
	if req.ResumeWorkspacePath == "" && req.ResumeHarnessSessionID == "" {
		for _, name := range []string{"runtime", "exports"} {
			if e := os.RemoveAll(filepath.Join(dir, name)); e != nil {
				return e
			}
		}
	}
	cfg := Config{Servers: []map[string]any{}}
	add := func(name, url, token string) {
		if url == "" {
			return
		}
		headers := map[string]string{}
		if token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		cfg.Servers = append(cfg.Servers, map[string]any{"name": name, "type": "http", "url": url, "headers": headers, "expose": "direct"})
	}
	add("runner-bridge", runnerURL, req.RunnerMCPToken)
	add("chetter", chetterURL, chetterToken)
	seen := map[string]bool{"runner-bridge": true, "chetter": true}
	for _, ep := range req.McpEndpoints {
		if seen[ep.Name] {
			return fmt.Errorf("duplicate/reserved MCP endpoint %q", ep.Name)
		}
		seen[ep.Name] = true
		servers := map[string]any{}
		if e := mcpconfig.AddClaudeServers(servers, []task.MCPEndpoint{ep}); e != nil {
			return e
		}
		s := servers[ep.Name].(map[string]any)
		s["name"] = ep.Name
		s["expose"] = "direct"
		cfg.Servers = append(cfg.Servers, s)
	}
	save := func(path string, data []byte) error {
		if st, e := os.Lstat(path); e == nil && !st.Mode().IsRegular() {
			return fmt.Errorf("niffler config path is not a regular file: %s", path)
		} else if e != nil && !os.IsNotExist(e) {
			return e
		}
		return mcpconfig.WritePrivateFile(path, data)
	}
	raw, e := json.Marshal(cfg)
	if e != nil {
		return e
	}
	if e = save(filepath.Join(dir, "mcp.json"), raw); e != nil {
		return e
	}
	if req.AgentDefinition != "" {
		if e = save(filepath.Join(dir, "agent.md"), []byte(req.AgentDefinition)); e != nil {
			return e
		}
	} else if e = os.Remove(filepath.Join(dir, "agent.md")); e != nil && !os.IsNotExist(e) {
		return e
	}
	skills := filepath.Join(dir, "skills")
	if e = os.MkdirAll(skills, 0700); e != nil {
		return e
	}
	if e = realDirectory(skills); e != nil {
		return e
	}
	for name, body := range req.SkillDefinitions {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") {
			return fmt.Errorf("invalid skill name %q", name)
		}
		d := filepath.Join(skills, name)
		if e = os.MkdirAll(d, 0700); e != nil {
			return e
		}
		if e = realDirectory(d); e != nil {
			return e
		}
		if e = os.RemoveAll(d); e != nil {
			return e
		}
		if e = os.MkdirAll(d, 0700); e != nil {
			return e
		}
		if e = skilltar.Extract(body, d); e != nil {
			return e
		}
	}
	return nil
}
func realDirectory(path string) error {
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("niffler directory must not be a symlink: %s", path)
	}
	return nil
}
func (*Niffler) Env(ws, secret string, req task.TaskRequest) map[string]string {
	protocol := "openai-chat"
	if req.ProviderAPI == "anthropic-messages" {
		protocol = "anthropic"
	}
	base := strings.TrimRight(req.ProviderBaseURL, "/")
	if protocol == "openai-chat" && !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	providers, _ := json.Marshal(map[string]any{"chetter": map[string]any{"baseUrl": base, "apiKey": os.Getenv(req.ProviderAPIKeyEnv), "model": req.ModelID, "protocol": protocol}})
	return map[string]string{"NIF_ROOT": filepath.Join(ws, ".niffler", "runtime"), "NIF_LLM_PROVIDERS": string(providers), "NIF_AUTO_APPROVE": "1", "NIF_NATS_SPAWN": "1", "CHETTER_NIFFLER_PROXY_TOKEN": secret, "XDG_CONFIG_HOME": filepath.Join(ws, ".niffler", "config"), "XDG_CACHE_HOME": filepath.Join(ws, ".niffler", "cache"), "XDG_DATA_HOME": filepath.Join(ws, ".niffler", "data")}
}
func auth(r *http.Request, secret string) { r.Header.Set("Authorization", "Bearer "+secret) }
func (*Niffler) WaitForReady(ctx context.Context, base, secret string, timeout time.Duration) error {
	if timeout < 90*time.Second {
		timeout = 90 * time.Second
	}
	return transport.WaitForReady(ctx, base, "/config", func(r *http.Request) { auth(r, secret) }, timeout, "niffler")
}
func request(ctx context.Context, base, path, secret string, body any) (*http.Response, error) {
	raw, e := json.Marshal(body)
	if e != nil {
		return nil, e
	}
	r, e := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(raw))
	if e != nil {
		return nil, e
	}
	auth(r, secret)
	r.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(r)
}
func decode(resp *http.Response, e error, out any) error {
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = json.Unmarshal(b, out)
		return fmt.Errorf("niffler HTTP %d: %s", resp.StatusCode, b)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}
func (*Niffler) CreateSession(ctx context.Context, base, secret string) (string, error) {
	var out struct {
		ID string `json:"session_id"`
	}
	r, e := request(ctx, base, "/session", secret, map[string]any{})
	if e = decode(r, e, &out); e != nil {
		return "", e
	}
	if out.ID == "" {
		return "", fmt.Errorf("niffler returned no session ID")
	}
	return out.ID, nil
}

type turnReply struct {
	Summary string    `json:"summary"`
	TurnID  string    `json:"turnId"`
	Outcome string    `json:"outcome"`
	Usage   TurnUsage `json:"usage"`
}

func (n *Niffler) SendPrompt(ctx context.Context, base, sid, secret string, req task.TaskRequest, ws string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	prompt := req.Prompt
	if len(req.Skills) > 0 {
		prompt += "\n\nUse these skills when applicable: " + strings.Join(req.Skills, ", ")
	}
	var out turnReply
	r, e := request(ctx, base, "/session/"+sid+"/message", secret, map[string]any{"prompt": prompt, "resume_session_id": req.ResumeHarnessSessionID, "model": req.ModelID, "thinking": req.VariantID})
	e = decode(r, e, &out)
	// Failures still report the rounds that completed. Reply, SSE and abort
	// carry the same turnId: accounting is idempotent, never a max/delta guess.
	n.account(out.TurnID, out.Usage.TaskUsage())
	if e != nil {
		return "", e
	}
	if out.TurnID == "" || out.Outcome != "success" {
		return "", fmt.Errorf("niffler turn outcome: %s", out.Outcome)
	}
	return out.Summary, nil
}
func (n *Niffler) AbortSession(ctx context.Context, base, sid, secret string) error {
	var out turnReply
	r, e := request(ctx, base, "/session/"+sid+"/abort", secret, map[string]any{})
	e = decode(r, e, &out)
	n.account(out.TurnID, out.Usage.TaskUsage())
	return e
}
func (*Niffler) ReadSessionExport(ws, sid string) (string, error) {
	b, e := os.ReadFile(filepath.Join(ws, ".niffler", "exports", sid+".md"))
	return string(b), e
}
func (n *Niffler) account(id string, delta task.TokenUsage) {
	if id == "" {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.accounted == nil {
		n.accounted = map[string]bool{}
	}
	if n.accounted[id] {
		return
	}
	n.accounted[id] = true
	n.usage.InputTokens += delta.InputTokens
	n.usage.OutputTokens += delta.OutputTokens
	n.usage.CacheReadTokens += delta.CacheReadTokens
	n.usage.CacheWriteTokens += delta.CacheWriteTokens
	n.usage.ReasoningTokens += delta.ReasoningTokens
	if n.tokens != nil {
		n.tokens(delta)
	}
}
func (n *Niffler) WatchEvents(ctx context.Context, id, base, secret string, publish func(string, string), tokens func(task.TokenUsage)) {
	n.mu.Lock()
	if n.tokens == nil && tokens != nil {
		tokens(n.usage)
	}
	n.tokens = tokens
	n.mu.Unlock()
	// A slow/disconnected stream may reconnect and replay; turnId accounting
	// prevents replay, final result and abort from charging the same turn twice.
	for ctx.Err() == nil {
		r, e := http.NewRequestWithContext(ctx, http.MethodGet, base+"/event", nil)
		if e != nil {
			return
		}
		auth(r, secret)
		resp, e := http.DefaultClient.Do(r)
		if e == nil && resp.StatusCode == 200 {
			reader := transport.NewEventReader(resp.Body)
			for {
				ev, e := reader.Read()
				if e != nil {
					break
				}
				var p struct {
					Message string `json:"message"`
					turnReply
				}
				if json.Unmarshal([]byte(ev.Data), &p) != nil {
					continue
				}
				if ev.Type == "usage" {
					n.account(p.TurnID, p.Usage.TaskUsage())
				} else if p.Message != "" {
					publish("running", p.Message)
				}
			}
		}
		if resp != nil {
			resp.Body.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}
