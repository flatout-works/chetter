// codex-serve-proxy adapts Codex App Server's JSON-RPC protocol to Chetter's
// common HTTP/SSE harness contract. It deliberately owns one App Server per
// task container so Codex state remains inside that task's workspace.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type sseEvent struct {
	Type string
	Data string
}

type session struct {
	mu        sync.Mutex
	id        string
	turnID    string
	prompt    string
	summary   strings.Builder
	runErr    string
	done      chan struct{}
	completed bool
	events    chan sseEvent
	lastUsage tokenUsage
}

type tokenUsage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CacheReadTokens int64 `json:"cache_read_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
}

type terminalEvent struct {
	Status  string     `json:"status"`
	Summary string     `json:"summary,omitempty"`
	Error   string     `json:"error,omitempty"`
	Usage   tokenUsage `json:"usage"`
}

type appServer struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	nextID   int
	pending  map[string]chan rpcMessage
	sessions map[string]*session
	first    *session
	firstCh  chan struct{}
}

type server struct {
	app      *appServer
	password string
}

func main() {
	port := flag.Int("port", 9999, "HTTP server port")
	flag.Parse()

	app, err := startAppServer()
	if err != nil {
		slog.Error("start Codex App Server", "err", err)
		os.Exit(1)
	}
	defer app.close()
	if err := app.initialize(); err != nil {
		slog.Error("initialize Codex App Server", "err", err)
		os.Exit(1)
	}

	password := os.Getenv("CODEX_SERVE_PROXY_TOKEN")
	if password == "" {
		password = generatePassword()
	}
	srv := &server{app: app, password: password}
	mux := http.NewServeMux()
	mux.HandleFunc("/config", srv.withAuth(srv.handleConfig))
	mux.HandleFunc("/session", srv.withAuth(srv.handleCreateSession))
	mux.HandleFunc("/session/", srv.withAuth(srv.handleSession))
	mux.HandleFunc("/event", srv.withAuth(srv.handleEvents))

	httpServer := &http.Server{Addr: fmt.Sprintf(":%d", *port), Handler: mux}
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server", "err", err)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
}

func startAppServer() (*appServer, error) {
	cmd := exec.Command("codex", "app-server", "--listen", "stdio://")
	cmd.Dir = "/workspace"
	cmd.Env = os.Environ()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}
	app := &appServer{
		cmd:      cmd,
		stdin:    stdin,
		pending:  make(map[string]chan rpcMessage),
		sessions: make(map[string]*session),
		firstCh:  make(chan struct{}),
	}
	go app.readLoop(stdout)
	go pipeStderr(stderr)
	return app, nil
}

func (a *appServer) close() {
	a.mu.Lock()
	cmd := a.cmd
	a.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

func (a *appServer) initialize() error {
	if _, err := a.call(context.Background(), "initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    "chetter",
			"title":   "Chetter",
			"version": "1.0.0",
		},
		"capabilities": map[string]bool{
			"experimentalApi": true,
		},
	}); err != nil {
		return err
	}
	return a.notify("initialized", map[string]any{})
}

func (a *appServer) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	a.mu.Lock()
	a.nextID++
	id := fmt.Sprintf("%d", a.nextID)
	responseCh := make(chan rpcMessage, 1)
	a.pending[id] = responseCh
	a.mu.Unlock()

	if err := a.write(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		a.mu.Lock()
		delete(a.pending, id)
		a.mu.Unlock()
		return nil, err
	}
	select {
	case response := <-responseCh:
		if response.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, response.Error.Message)
		}
		return response.Result, nil
	case <-ctx.Done():
		a.mu.Lock()
		delete(a.pending, id)
		a.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (a *appServer) notify(method string, params any) error {
	return a.write(map[string]any{"method": method, "params": params})
}

func (a *appServer) write(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stdin == nil {
		return fmt.Errorf("codex app server is not running")
	}
	_, err = a.stdin.Write(append(data, '\n'))
	return err
}

func (a *appServer) readLoop(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		var message rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			slog.Warn("invalid Codex App Server message", "err", err)
			continue
		}
		if len(message.ID) > 0 && message.Method == "" {
			a.deliverResponse(message)
			continue
		}
		if len(message.ID) > 0 && message.Method != "" {
			a.rejectServerRequest(message)
			continue
		}
		if message.Method != "" {
			a.handleNotification(message)
		}
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("Codex App Server output", "err", err)
		a.fail(fmt.Errorf("codex app server output: %w", err))
		return
	}
	a.fail(io.EOF)
}

func (a *appServer) fail(cause error) {
	message := "codex app server exited: " + cause.Error()
	a.mu.Lock()
	a.stdin = nil
	pending := a.pending
	a.pending = make(map[string]chan rpcMessage)
	sessions := make([]*session, 0, len(a.sessions))
	for _, s := range a.sessions {
		sessions = append(sessions, s)
	}
	a.mu.Unlock()

	for _, responseCh := range pending {
		responseCh <- rpcMessage{Error: &rpcError{Code: -32001, Message: message}}
	}
	for _, s := range sessions {
		s.fail(message)
	}
}

func (a *appServer) deliverResponse(message rpcMessage) {
	id := rpcID(message.ID)
	a.mu.Lock()
	responseCh := a.pending[id]
	delete(a.pending, id)
	a.mu.Unlock()
	if responseCh != nil {
		responseCh <- message
	}
}

func (a *appServer) rejectServerRequest(message rpcMessage) {
	_ = a.write(map[string]any{
		"id": message.ID,
		"error": map[string]any{
			"code":    -32000,
			"message": "Chetter runs non-interactively and cannot approve this request",
		},
	})
}

func (a *appServer) handleNotification(message rpcMessage) {
	threadID := notificationThreadID(message.Params)
	if threadID == "" {
		return
	}
	a.mu.Lock()
	s := a.sessions[threadID]
	a.mu.Unlock()
	if s == nil {
		return
	}

	switch message.Method {
	case "item/agentMessage/delta":
		var event struct {
			Delta string `json:"delta"`
		}
		if json.Unmarshal(message.Params, &event) == nil && event.Delta != "" {
			s.mu.Lock()
			s.summary.WriteString(event.Delta)
			s.mu.Unlock()
			s.publish(sseEvent{Type: "codex.delta", Data: event.Delta})
		}
	case "item/started", "item/completed":
		if detail := itemDetail(message.Params); detail != "" {
			s.publish(sseEvent{Type: "codex.activity", Data: detail})
		}
	case "thread/tokenUsage/updated":
		if usage, ok := tokenUsageDelta(s, message.Params); ok {
			data, _ := json.Marshal(usage)
			s.publish(sseEvent{Type: "codex.usage", Data: string(data)})
		}
	case "turn/completed":
		a.completeTurn(s, message.Params)
	}
}

func (a *appServer) completeTurn(s *session, params json.RawMessage) {
	var event struct {
		Turn struct {
			ID     string `json:"id"`
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	if json.Unmarshal(params, &event) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turnID != "" && event.Turn.ID != s.turnID || s.completed {
		return
	}
	s.completed = true
	if event.Turn.Status != "completed" {
		s.runErr = event.Turn.Status
		if event.Turn.Error != nil && event.Turn.Error.Message != "" {
			s.runErr = event.Turn.Error.Message
		}
	}
	writeSessionExport(s)
	close(s.done)
}

func (a *appServer) newSession(id string) *session {
	s := &session{id: id, events: make(chan sseEvent, 512), done: make(chan struct{})}
	a.mu.Lock()
	a.sessions[id] = s
	if a.first == nil {
		a.first = s
		close(a.firstCh)
	}
	a.mu.Unlock()
	return s
}

func (a *appServer) session(ctx context.Context, id string, resume bool) (*session, error) {
	a.mu.Lock()
	s := a.sessions[id]
	a.mu.Unlock()
	if s != nil {
		return s, nil
	}
	if !resume {
		return nil, fmt.Errorf("session %s not found", id)
	}
	if _, err := a.call(ctx, "thread/resume", map[string]any{"threadId": id}); err != nil {
		return nil, err
	}
	return a.newSession(id), nil
}

func (a *appServer) firstSession(ctx context.Context) (*session, error) {
	a.mu.Lock()
	s := a.first
	a.mu.Unlock()
	if s != nil {
		return s, nil
	}
	select {
	case <-a.firstCh:
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.first, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *session) publish(event sseEvent) {
	select {
	case s.events <- event:
	default:
		slog.Warn("dropping slow Codex event consumer", "session", s.id, "type", event.Type)
	}
}

func (s *session) fail(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.completed {
		return
	}
	s.completed = true
	s.runErr = message
	writeSessionExport(s)
	close(s.done)
}

func (s *session) begin(prompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prompt = prompt
	s.turnID = ""
	s.summary.Reset()
	s.runErr = ""
	s.completed = false
	s.done = make(chan struct{})
}

func (s *session) result() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runErr != "" {
		return "", fmt.Errorf("codex turn failed: %s", s.runErr)
	}
	return s.summary.String(), nil
}

func (s *session) setTurnID(turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turnID = turnID
}

func (s *session) activeTurn() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turnID
}

func (srv *server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != basicAuthHeader(srv.password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="codex-serve-proxy"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (srv *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"harness":"codex"}`)
}

func (srv *server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result, err := srv.app.call(r.Context(), "thread/start", map[string]any{
		"cwd":            "/workspace",
		"approvalPolicy": "never",
		"sandbox":        "workspace-write",
	})
	if err != nil {
		srv.writeError(w, err)
		return
	}
	var response struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(result, &response); err != nil || response.Thread.ID == "" {
		srv.writeError(w, fmt.Errorf("thread/start returned no thread ID"))
		return
	}
	srv.app.newSession(response.Thread.ID)
	writeJSON(w, map[string]string{"session_id": response.Thread.ID})
}

func (srv *server) handleSession(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/session/"), "/")
	if len(parts) != 2 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	switch {
	case parts[1] == "message" && r.Method == http.MethodPost:
		srv.handleMessage(w, r, parts[0])
	case parts[1] == "abort" && r.Method == http.MethodPost:
		srv.handleAbort(w, r, parts[0])
	case parts[1] == "export" && r.Method == http.MethodGet:
		srv.handleExport(w, r, parts[0])
	default:
		http.NotFound(w, r)
	}
}

type messageRequest struct {
	Prompt string `json:"prompt"`
	Model  string `json:"model"`
	Resume bool   `json:"resume"`
}

func (srv *server) handleMessage(w http.ResponseWriter, r *http.Request, sessionID string) {
	var request messageRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || strings.TrimSpace(request.Prompt) == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}
	s, err := srv.app.session(r.Context(), sessionID, request.Resume)
	if err != nil {
		srv.writeError(w, err)
		return
	}
	s.begin(request.Prompt)
	params := map[string]any{
		"threadId": sessionID,
		"input":    []map[string]string{{"type": "text", "text": request.Prompt}},
	}
	if request.Model != "" {
		params["model"] = request.Model
	}
	result, err := srv.app.call(r.Context(), "turn/start", params)
	if err != nil {
		srv.writeError(w, err)
		return
	}
	var response struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(result, &response) != nil || response.Turn.ID == "" {
		srv.writeError(w, fmt.Errorf("turn/start returned no turn ID"))
		return
	}
	s.setTurnID(response.Turn.ID)
	select {
	case <-s.done:
		summary, err := s.result()
		if err != nil {
			srv.writeError(w, err)
			return
		}
		writeJSON(w, map[string]string{"status": "completed", "summary": summary})
	case <-r.Context().Done():
	}
}

func (srv *server) handleAbort(w http.ResponseWriter, r *http.Request, sessionID string) {
	s, err := srv.app.session(r.Context(), sessionID, false)
	if err != nil {
		writeJSON(w, map[string]string{"status": "nothing-to-abort"})
		return
	}
	turnID := s.activeTurn()
	if turnID == "" {
		writeJSON(w, map[string]string{"status": "nothing-to-abort"})
		return
	}
	if _, err := srv.app.call(r.Context(), "turn/interrupt", map[string]string{"threadId": sessionID, "turnId": turnID}); err != nil {
		srv.writeError(w, err)
		return
	}
	writeJSON(w, map[string]string{"status": "aborted"})
}

func (srv *server) handleExport(w http.ResponseWriter, r *http.Request, sessionID string) {
	path := sessionExportPath(sessionID)
	data, err := os.ReadFile(path)
	if err != nil {
		srv.writeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	_, _ = w.Write(data)
}

func (srv *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	s, err := srv.app.firstSession(r.Context())
	if err != nil {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.done:
			// Drain queued progress before the authoritative terminal snapshot.
			for {
				select {
				case event := <-s.events:
					_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data)
					flusher.Flush()
				default:
					data, _ := json.Marshal(codexTerminalEvent(s))
					_, _ = fmt.Fprintf(w, "event: done\ndata: %s\n\n", data)
					flusher.Flush()
					return
				}
			}
		case event := <-s.events:
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data)
			flusher.Flush()
		}
	}
}

func codexTerminalEvent(s *session) terminalEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := "completed"
	if s.runErr != "" {
		status = "failed"
	}
	return terminalEvent{
		Status:  status,
		Summary: s.summary.String(),
		Error:   s.runErr,
		Usage:   s.lastUsage,
	}
}

func writeSessionExport(s *session) {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		home = "/workspace/.codex"
	}
	var b strings.Builder
	b.WriteString("# Codex Session\n\n")
	fmt.Fprintf(&b, "Thread: `%s`\n\n", s.id)
	b.WriteString("## User\n\n")
	b.WriteString(s.prompt)
	b.WriteString("\n\n## Assistant\n\n")
	b.WriteString(s.summary.String())
	if s.runErr != "" {
		b.WriteString("\n\n## Error\n\n")
		b.WriteString(s.runErr)
	}
	if err := os.WriteFile(sessionExportPathForHome(home, s.id), []byte(b.String()), 0600); err != nil {
		slog.Warn("write Codex session export", "session", s.id, "err", err)
	}
}

func sessionExportPath(sessionID string) string {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		home = "/workspace/.codex"
	}
	return sessionExportPathForHome(home, sessionID)
}

func sessionExportPathForHome(home, sessionID string) string {
	return filepath.Join(home, "session-"+sessionID+".md")
}

func notificationThreadID(params json.RawMessage) string {
	var value struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(params, &value)
	return value.ThreadID
}

func itemDetail(params json.RawMessage) string {
	var value struct {
		Item map[string]any `json:"item"`
	}
	if json.Unmarshal(params, &value) != nil || value.Item == nil {
		return ""
	}
	typ, _ := value.Item["type"].(string)
	switch typ {
	case "commandExecution":
		if command, _ := value.Item["command"].(string); command != "" {
			return "command: " + command
		}
	case "mcpToolCall":
		if name, _ := value.Item["tool"].(string); name != "" {
			return "MCP tool: " + name
		}
	}
	return typ
}

func tokenUsageDelta(s *session, params json.RawMessage) (tokenUsage, bool) {
	var value struct {
		TokenUsage struct {
			Total struct {
				InputTokens     int64 `json:"inputTokens"`
				OutputTokens    int64 `json:"outputTokens"`
				CacheReadTokens int64 `json:"cachedInputTokens"`
				ReasoningTokens int64 `json:"reasoningOutputTokens"`
			} `json:"total"`
		} `json:"tokenUsage"`
	}
	if json.Unmarshal(params, &value) != nil {
		return tokenUsage{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := tokenUsage{
		InputTokens:     value.TokenUsage.Total.InputTokens,
		OutputTokens:    value.TokenUsage.Total.OutputTokens,
		CacheReadTokens: value.TokenUsage.Total.CacheReadTokens,
		ReasoningTokens: value.TokenUsage.Total.ReasoningTokens,
	}
	delta := tokenUsage{
		InputTokens:     next.InputTokens - s.lastUsage.InputTokens,
		OutputTokens:    next.OutputTokens - s.lastUsage.OutputTokens,
		CacheReadTokens: next.CacheReadTokens - s.lastUsage.CacheReadTokens,
		ReasoningTokens: next.ReasoningTokens - s.lastUsage.ReasoningTokens,
	}
	s.lastUsage = next
	return delta, delta.InputTokens != 0 || delta.OutputTokens != 0 || delta.CacheReadTokens != 0 || delta.ReasoningTokens != 0
}

func rpcID(raw json.RawMessage) string {
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return id
	}
	return strings.Trim(string(raw), `"`)
}

func basicAuthHeader(secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("opencode:"+secret))
}

func generatePassword() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (srv *server) writeError(w http.ResponseWriter, err error) {
	slog.Warn("Codex proxy request failed", "err", err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func pipeStderr(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		slog.Info("Codex App Server", "line", scanner.Text())
	}
}
