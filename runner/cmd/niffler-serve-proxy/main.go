// niffler-serve-proxy retains Chetter's authenticated HTTP lifecycle while
// delegating turns, MCP bootstrap, cancellation and export to native cli run.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
)

type envelope struct {
	V      int             `json:"v"`
	ID     string          `json:"id"`
	Kind   string          `json:"kind"`
	Tool   string          `json:"tool,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	Error  *wireError      `json:"error,omitempty"`
	Caller string          `json:"caller,omitempty"`
}
type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type event struct {
	Type string
	Data json.RawMessage
}
type nativeResult struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId"`
	TurnID    string          `json:"turnId"`
	Outcome   string          `json:"outcome"`
	Reply     string          `json:"reply"`
	TurnError string          `json:"turnError"`
	Usage     json.RawMessage `json:"usage"`
}
type bridge struct {
	nc                        *nats.Conn
	workspace, root, password string
	mu                        sync.Mutex
	sessions                  map[string]string
	active                    *exec.Cmd
	finished                  chan struct{}
	last                      nativeResult
	busy                      bool
	history                   []event
	listeners                 map[chan event]bool
	secrets                   []string
	// Tests inject a command; production always executes the trusted runtime CLI.
	command func(string, ...string) *exec.Cmd
}

func newID() string {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func (b *bridge) call(ctx context.Context, component, tool string, args any) (json.RawMessage, error) {
	raw, e := json.Marshal(args)
	if e != nil {
		return nil, e
	}
	data, _ := json.Marshal(envelope{V: 1, ID: newID(), Kind: "call", Tool: tool, Args: raw, Caller: "chetter"})
	m, e := b.nc.RequestWithContext(ctx, "svc."+component+".call", data)
	if e != nil {
		return nil, e
	}
	var out envelope
	if e = json.Unmarshal(m.Data, &out); e != nil {
		return nil, e
	}
	if out.Kind != "result" {
		if out.Error != nil {
			return nil, fmt.Errorf("niffler %s: %s", out.Error.Code, out.Error.Message)
		}
		return nil, errors.New("invalid Niffler result envelope")
	}
	return out.Args, nil
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func validID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
func (b *bridge) emit(kind string, p any) {
	raw, e := json.Marshal(p)
	if e != nil {
		return
	}
	ev := event{kind, raw}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.history = append(b.history, ev)
	if len(b.history) > 512 {
		b.history = b.history[len(b.history)-512:]
	}
	for ch := range b.listeners {
		select {
		case ch <- ev:
		default:
			delete(b.listeners, ch)
			close(ch)
		}
	}
}
func (b *bridge) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /config", func(w http.ResponseWriter, r *http.Request) {
		ctx, c := context.WithTimeout(r.Context(), 2*time.Second)
		defer c()
		if _, e := b.call(ctx, "core", "catalog", map[string]any{"op": "list"}); e != nil {
			writeJSON(w, 503, map[string]any{"error": e.Error()})
			return
		}
		writeJSON(w, 200, map[string]any{"ready": true})
	})
	mux.HandleFunc("POST /session", func(w http.ResponseWriter, r *http.Request) {
		id := newID()
		b.mu.Lock()
		b.sessions[id] = id
		b.mu.Unlock()
		writeJSON(w, 200, map[string]any{"session_id": id})
	})
	mux.HandleFunc("POST /session/{id}/message", b.message)
	mux.HandleFunc("POST /session/{id}/abort", func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		_, ok := b.sessions[r.PathValue("id")]
		cmd, done := b.active, b.finished
		b.mu.Unlock()
		if !ok {
			writeJSON(w, 404, map[string]any{"error": "unknown session"})
			return
		}
		if cmd != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		if done != nil {
			select {
			case <-done:
			case <-r.Context().Done():
				writeJSON(w, 504, map[string]any{"error": "cancel finalization timed out"})
				return
			}
		}
		b.mu.Lock()
		last := b.last
		b.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ok": true, "turnId": last.TurnID, "outcome": last.Outcome, "usage": last.Usage})
	})
	mux.HandleFunc("GET /event", b.events)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if b.password == "" || subtle.ConstantTimeCompare([]byte(token), []byte(b.password)) != 1 {
			writeJSON(w, 401, map[string]any{"error": "unauthorized"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}
func (b *bridge) message(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Prompt   string `json:"prompt"`
		Resume   string `json:"resume_session_id"`
		Model    string `json:"model"`
		Thinking string `json:"thinking"`
	}
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&in); e != nil || strings.TrimSpace(in.Prompt) == "" {
		writeJSON(w, 400, map[string]any{"error": "nonempty prompt required"})
		return
	}
	if in.Resume != "" && !validID(in.Resume) {
		writeJSON(w, 400, map[string]any{"error": "invalid resume session ID"})
		return
	}
	b.mu.Lock()
	id, ok := b.sessions[r.PathValue("id")]
	if !ok && in.Resume == r.PathValue("id") && validID(in.Resume) {
		id = in.Resume
		ok = true
	}
	if !ok {
		b.mu.Unlock()
		writeJSON(w, 404, map[string]any{"error": "unknown session"})
		return
	}
	if b.busy {
		b.mu.Unlock()
		writeJSON(w, 409, map[string]any{"error": "busy"})
		return
	}
	if in.Resume != "" {
		id = in.Resume
	}
	b.sessions[r.PathValue("id")] = id
	b.busy = true
	b.finished = make(chan struct{})
	done := b.finished
	b.mu.Unlock()
	defer func() { b.mu.Lock(); b.active = nil; b.busy = false; close(done); b.mu.Unlock() }()
	if in.Resume != "" {
		ctx, c := context.WithTimeout(r.Context(), 10*time.Second)
		raw, e := b.call(ctx, "store", "get", map[string]any{"kind": "conversation", "id": id})
		c()
		var h struct {
			OK bool `json:"ok"`
		}
		if e != nil || json.Unmarshal(raw, &h) != nil || !h.OK {
			writeJSON(w, 404, map[string]any{"error": "resume conversation does not exist"})
			return
		}
	}
	result, e := b.runNative(r.Context(), id, in.Prompt, in.Model, in.Thinking, r.PathValue("id"))
	b.mu.Lock()
	b.last = result
	b.mu.Unlock()
	if e != nil {
		writeJSON(w, 502, map[string]any{"error": b.redact(e.Error()), "turnId": result.TurnID, "outcome": result.Outcome, "usage": result.Usage})
		return
	}
	writeJSON(w, 200, map[string]any{"summary": b.redact(result.Reply), "turnId": result.TurnID, "outcome": result.Outcome, "usage": result.Usage})
}
func (b *bridge) runNative(ctx context.Context, id, prompt, model, thinking, exportID string) (nativeResult, error) {
	// CLI export/MCP inputs are private scratch files in the task runtime. The
	// driver exports raw canonical JSONL; Chetter renders/redacts markdown only.
	dir, e := os.MkdirTemp(filepath.Join(b.root, "var"), "chetter-turn-")
	if e != nil {
		return nativeResult{}, e
	}
	defer os.RemoveAll(dir)
	raw, e := os.ReadFile(filepath.Join(b.workspace, ".niffler", "mcp.json"))
	if e != nil {
		return nativeResult{}, e
	}
	var cfg struct {
		Servers []map[string]any `json:"servers"`
	}
	if e = json.Unmarshal(raw, &cfg); e != nil {
		return nativeResult{}, e
	}
	raw, e = json.Marshal(cfg.Servers)
	if e != nil {
		return nativeResult{}, e
	}
	mcpFile := filepath.Join(dir, "mcp.json")
	if e = privateWrite(mcpFile, raw); e != nil {
		return nativeResult{}, e
	}
	transcript := filepath.Join(dir, "messages.jsonl")
	args := []string{"run", "--root=" + b.root, "--bus=" + b.nc.ConnectedUrl(), "--session=" + id, "--cwd=" + b.workspace, "--provider=chetter", "--approvals=auto", "--mcp-file=" + mcpFile, "--export=" + transcript, "--timeout=86400", "--cancel-grace=5", "--prompt=" + prompt}
	if model != "" {
		args = append(args, "--model="+model)
	}
	if thinking != "" {
		args = append(args, "--thinking="+thinking)
	}
	factory := b.command
	if factory == nil {
		factory = exec.Command
	}
	cmd := factory(filepath.Join(b.root, "var", "bin", "cli"), args...)
	cmd.Dir = b.workspace
	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}
	cmd.Env = append(env, "NIF_ROOT="+b.root, "NIF_NATS_URL="+b.nc.ConnectedUrl())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	cmd.Stderr = os.Stderr
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return nativeResult{}, e
	}
	if e = cmd.Start(); e != nil {
		return nativeResult{}, e
	}
	b.mu.Lock()
	b.active = cmd
	b.mu.Unlock()
	stopped := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-stopped:
			case <-time.After(8 * time.Second):
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		case <-stopped:
		}
	}()
	var result nativeResult
	count := 0
	var protocolErr error
	progress := &progressBuffer{}
	defer func() { progress.flush(time.Now(), true, b.progressMessage) }()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	for scanner.Scan() {
		var line struct {
			Type    string          `json:"type"`
			Subject string          `json:"subject"`
			Data    json.RawMessage `json:"data"`
			Message string          `json:"message"`
		}
		if e = json.Unmarshal(scanner.Bytes(), &line); e != nil {
			protocolErr = fmt.Errorf("invalid native NDJSON: %w", e)
			_ = cmd.Process.Kill()
			break
		}
		switch line.Type {
		case "event":
			b.observeProgress(line.Subject, line.Data, id, progress, time.Now())
		case "mcp":
			b.emit("message", map[string]any{"message": "niffler MCP bootstrap"})
		case "error":
			protocolErr = errors.New(line.Message)
		case "result":
			count++
			if e = json.Unmarshal(scanner.Bytes(), &result); e != nil {
				protocolErr = e
			}
		}
	}
	if e = scanner.Err(); e != nil {
		protocolErr = e
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	close(stopped)
	if count != 1 {
		if protocolErr != nil {
			return result, protocolErr
		}
		return result, fmt.Errorf("native driver returned %d results: %v", count, waitErr)
	}
	if result.SessionID != id || result.TurnID == "" || len(result.Usage) == 0 || string(result.Usage) == "null" {
		return result, errors.New("native driver returned incomplete turn accounting")
	}
	b.emit("usage", map[string]any{"turnId": result.TurnID, "outcome": result.Outcome, "usage": result.Usage})
	text, e := renderTranscript(transcript)
	if e != nil {
		return result, fmt.Errorf("native transcript: %w", e)
	}
	exportDir := filepath.Join(b.workspace, ".niffler", "exports")
	if e = os.MkdirAll(exportDir, 0700); e != nil {
		return result, e
	}
	if e = requireDirectory(exportDir); e != nil {
		return result, e
	}
	if e = privateWrite(filepath.Join(exportDir, exportID+".md"), []byte(b.redact(text))); e != nil {
		return result, e
	}
	if protocolErr != nil {
		return result, protocolErr
	}
	if result.Outcome != "success" || result.TurnError != "" {
		detail := result.TurnError
		if detail == "" {
			detail = result.Outcome
		}
		return result, errors.New(detail)
	}
	if waitErr != nil {
		return result, fmt.Errorf("native driver failed after result: %w", waitErr)
	}
	return result, nil
}
func (b *bridge) progressMessage(message string) {
	b.emit("message", map[string]any{"message": b.redact(message)})
}
func (b *bridge) observe(subject string, data json.RawMessage, id string) {
	b.observeProgress(subject, data, id, &progressBuffer{}, time.Now())
}
func (b *bridge) observeProgress(subject string, data json.RawMessage, id string, progress *progressBuffer, now time.Time) {
	var p struct {
		SessionID string          `json:"sessionId"`
		TurnID    string          `json:"turnId"`
		Outcome   string          `json:"outcome"`
		Phase     string          `json:"phase"`
		Content   string          `json:"content"`
		Reasoning string          `json:"reasoning"`
		Tool      string          `json:"tool"`
		Usage     json.RawMessage `json:"usage"`
	}
	if json.Unmarshal(data, &p) != nil || p.SessionID != id {
		return
	}
	switch {
	case strings.HasSuffix(subject, ".token"):
		progress.token(now, p.Content, p.Reasoning, b.progressMessage)
	case strings.HasSuffix(subject, ".assistant"):
		progress.assistant(now, p.Content, b.progressMessage)
	case strings.HasSuffix(subject, ".toolcall"):
		progress.flush(now, true, b.progressMessage)
		b.emit("message", map[string]any{"message": fmt.Sprintf("niffler tool %s: %s", p.Phase, p.Tool)})
	case strings.HasSuffix(subject, ".turn") && p.Phase == "done":
		progress.flush(now, true, b.progressMessage)
		b.emit("usage", map[string]any{"turnId": p.TurnID, "outcome": p.Outcome, "usage": p.Usage})
	}
}
func renderTranscript(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	var text strings.Builder
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64<<10), 8<<20)
	for s.Scan() {
		var item struct {
			Message struct {
				Role      string          `json:"role"`
				Content   json.RawMessage `json:"content"`
				Reasoning string          `json:"reasoning"`
				Calls     json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		}
		if e = json.Unmarshal(s.Bytes(), &item); e != nil {
			return "", e
		}
		v := item.Message
		fmt.Fprintf(&text, "## %s\n\n", v.Role)
		var content string
		if json.Unmarshal(v.Content, &content) != nil {
			content = string(v.Content)
		}
		text.WriteString(content + "\n\n")
		if v.Reasoning != "" {
			text.WriteString("### Thinking\n\n" + v.Reasoning + "\n\n")
		}
		if len(v.Calls) > 0 {
			fmt.Fprintf(&text, "```json\n%s\n```\n\n", v.Calls)
		}
	}
	return text.String(), s.Err()
}
func (b *bridge) events(w http.ResponseWriter, r *http.Request) {
	f, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]any{"error": "stream unavailable"})
		return
	}
	ch := make(chan event, 128)
	b.mu.Lock()
	history := append([]event(nil), b.history...)
	b.listeners[ch] = true
	b.mu.Unlock()
	defer func() { b.mu.Lock(); delete(b.listeners, ch); b.mu.Unlock() }()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(200)
	f.Flush()
	send := func(ev event) bool {
		_, e := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data)
		f.Flush()
		return e == nil
	}
	for _, ev := range history {
		if !send(ev) {
			return
		}
	}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok || !send(ev) {
				return
			}
		case <-t.C:
			if _, e := io.WriteString(w, ": heartbeat\n\n"); e != nil {
				return
			}
			f.Flush()
		}
	}
}
func main() { os.Exit(run()) }
func run() int {
	port := flag.Int("port", 9999, "HTTP port")
	flag.Parse()
	password := os.Getenv("CHETTER_NIFFLER_PROXY_TOKEN")
	if password == "" {
		log.Print("CHETTER_NIFFLER_PROXY_TOKEN is required")
		return 1
	}
	workspace, e := os.Getwd()
	if e != nil {
		log.Print(e)
		return 1
	}
	root := os.Getenv("NIF_ROOT")
	if root == "" {
		root = filepath.Join(workspace, ".niffler", "runtime")
	}
	source := os.Getenv("CHETTER_NIFFLER_HOME")
	if source == "" {
		source = "/opt/niffler"
	}
	if e = prepareRuntime(source, root, workspace); e != nil {
		log.Print(e)
		return 1
	}
	if e = os.Remove(filepath.Join(root, "var", "nats-url")); e != nil && !os.IsNotExist(e) {
		log.Print(e)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	cmd := exec.Command(filepath.Join(root, "var", "bin", "niffler"))
	cmd.Dir = root
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	env := []string{}
	for _, v := range os.Environ() {
		k, _, _ := strings.Cut(v, "=")
		if k == "NIF_NATS_URL" || k == "NIF_AUTOSTART" {
			continue
		}
		env = append(env, v)
	}
	cmd.Env = append(env, "NIF_ROOT="+root, "NIF_NATS_SPAWN=1", "NIF_AUTO_APPROVE=1", "NIF_STORE_BACKEND=sqlite")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	if e = cmd.Start(); e != nil {
		log.Print(e)
		return 1
	}
	exited := make(chan struct{})
	var processErr error
	go func() { processErr = cmd.Wait(); close(exited) }()
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			<-exited
		}
	}()
	b := &bridge{workspace: workspace, root: root, password: password, sessions: map[string]string{}, listeners: map[chan event]bool{}}
	if e = b.loadSecrets(); e != nil {
		log.Print(e)
		return 1
	}
	startup, c := context.WithTimeout(ctx, 90*time.Second)
	defer c()
	for {
		raw, e := os.ReadFile(filepath.Join(root, "var", "nats-url"))
		if e == nil {
			nc, e := nats.Connect(strings.TrimSpace(string(raw)), nats.Timeout(time.Second), nats.NoReconnect())
			if e == nil {
				b.nc = nc
				probe, c := context.WithTimeout(startup, time.Second)
				catalog, e := b.call(probe, "core", "catalog", map[string]any{"op": "components"})
				c()
				var identity struct {
					Root string `json:"root"`
				}
				if e == nil && json.Unmarshal(catalog, &identity) == nil && filepath.Clean(identity.Root) == filepath.Clean(root) && strings.Contains(string(catalog), "mcp_add") && strings.Contains(string(catalog), "systemprompt") {
					break
				}
				nc.Close()
			}
		}
		select {
		case <-startup.Done():
			log.Print(startup.Err())
			return 1
		case <-exited:
			log.Print("Niffler exited: ", processErr)
			return 1
		case <-time.After(100 * time.Millisecond):
		}
	}
	defer b.nc.Close()
	srv := &http.Server{Addr: fmt.Sprintf(":%d", *port), Handler: b.handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute}
	failed := make(chan error, 1)
	go func() { failed <- srv.ListenAndServe() }()
	status := 0
	select {
	case <-ctx.Done():
	case <-exited:
		log.Print("Niffler exited: ", processErr)
		status = 1
	case e := <-failed:
		log.Print(e)
		status = 1
	}
	b.mu.Lock()
	active := b.active
	b.mu.Unlock()
	if active != nil {
		_ = active.Process.Signal(syscall.SIGTERM)
	}
	shutdown, c := context.WithTimeout(context.Background(), 10*time.Second)
	defer c()
	_ = srv.Shutdown(shutdown)
	_ = srv.Close()
	return status
}
