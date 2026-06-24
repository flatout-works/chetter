package main

import (
	"bufio"
	"context"
	"crypto/rand"
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
	"strings"
	"sync"
	"syscall"
	"time"
)

type session struct {
	mu        sync.Mutex
	id        string
	cmd       *exec.Cmd
	cancel    context.CancelFunc
	events    chan event
	done      chan struct{}
	prompt    string
	model     string
	resumeID  string
}

type event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type server struct {
	mu       sync.Mutex
	sessions map[string]*session
	password string
}

func main() {
	port := flag.Int("port", 9999, "HTTP server port")
	flag.Parse()

	password := generatePassword()

	srv := &server{
		sessions: make(map[string]*session),
		password: password,
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(handler))

	mux := http.NewServeMux()
	mux.HandleFunc("/health", withAuth(srv, srv.handleHealth))
	mux.HandleFunc("/config", withAuth(srv, srv.handleConfig))
	mux.HandleFunc("/session", withAuth(srv, srv.handleCreateSession))
	mux.HandleFunc("/session/", withAuth(srv, srv.handleSession))
	mux.HandleFunc("/event", withAuth(srv, srv.handleEvents))

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: mux,
	}

	go func() {
		slog.Info("claude-serve-proxy starting", "port", *port)
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	<-sigCh
	slog.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	srv.mu.Lock()
	for _, s := range srv.sessions {
		if s.cancel != nil {
			s.cancel()
		}
	}
	srv.mu.Unlock()
	httpServer.Shutdown(ctx)
}

func generatePassword() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func basicAuthHeader(secret string) string {
	return "Basic " + base64Encode([]byte("opencode:"+secret))
}

func base64Encode(data []byte) string {
	const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var enc []byte

	for i := 0; i < len(data); i += 3 {
		var v uint32
		v |= uint32(data[i]) << 16
		if i+1 < len(data) {
			v |= uint32(data[i+1]) << 8
		}
		if i+2 < len(data) {
			v |= uint32(data[i+2])
		}

		enc = append(enc, b64[(v>>18)&0x3F])
		enc = append(enc, b64[(v>>12)&0x3F])
		if i+1 < len(data) {
			enc = append(enc, b64[(v>>6)&0x3F])
		} else {
			enc = append(enc, '=')
		}
		if i+2 < len(data) {
			enc = append(enc, b64[v&0x3F])
		} else {
			enc = append(enc, '=')
		}
	}
	return string(enc)
}

func withAuth(srv *server, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if srv.password != "" {
			header := fmt.Sprintf("Basic %s", basicAuthHeader(srv.password))
			if r.Header.Get("Authorization") != header {
				w.Header().Set("WWW-Authenticate", `Basic realm="claude-serve-proxy"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func (srv *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, `{"status":"ok"}`)
}

func (srv *server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, `{"harness":"claude"}`)
}

func (srv *server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := generateSessionID()
	s := &session{
		id:     id,
		events: make(chan event, 1024),
		done:   make(chan struct{}),
	}

	srv.mu.Lock()
	srv.sessions[id] = s
	srv.mu.Unlock()

	slog.Info("session created", "id", id)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"session_id": id})
}

func (srv *server) handleSession(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/session/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	sid := parts[0]

	srv.mu.Lock()
	s, ok := srv.sessions[sid]
	srv.mu.Unlock()

	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case action == "message" && r.Method == http.MethodPost:
		srv.handleSendPrompt(w, r, s)
	case action == "abort" && r.Method == http.MethodPost:
		srv.handleAbort(w, r, s)
	case action == "export" && r.Method == http.MethodGet:
		srv.handleExport(w, r, s)
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

type messageRequest struct {
	Prompt     string   `json:"prompt"`
	Model      string   `json:"model"`
	Skills     []string `json:"skills"`
	Agent      string   `json:"agent"`
	ResumeID   string   `json:"resume_session_id"`
}

func (srv *server) handleSendPrompt(w http.ResponseWriter, r *http.Request, s *session) {
	s.mu.Lock()
	if s.cmd != nil {
		s.mu.Unlock()
		http.Error(w, "session already running", http.StatusConflict)
		return
	}
	s.mu.Unlock()

	var req messageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.prompt = req.Prompt
	s.model = req.Model
	if req.ResumeID != "" {
		s.resumeID = req.ResumeID
	}
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()

	prompt := req.Prompt
	if len(req.Skills) > 0 {
		prompt = fmt.Sprintf("You have access to the following skills: %s. Use them when relevant.\n\n%s",
			strings.Join(req.Skills, ", "), prompt)
	}

	model := req.Model
	if model == "" {
		model = "claude-sonnet-4-5"
	}

	args := []string{
		"claude",
		"-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--model", model,
		"--max-turns", "100",
	}
	if req.Agent != "" {
		systemPrompt := resolveAgentFile(req.Agent)
		if systemPrompt != "" {
			args = append(args, "--system-prompt", systemPrompt)
		}
	}
	if req.ResumeID != "" {
		args = append(args, "--resume", req.ResumeID)
	}

	slog.Info("starting claude", "session", s.id, "model", model, "resume", req.ResumeID)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = "/workspace"
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env,
		"CLAUDE_CONFIG_DIR=/workspace/.claude",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"CLAUDE_CODE_ATTRIBUTION_HEADER=0",
	)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		srv.sendError(w, fmt.Sprintf("stdout pipe: %v", err))
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		srv.sendError(w, fmt.Sprintf("stderr pipe: %v", err))
		return
	}

	if err := cmd.Start(); err != nil {
		cancel()
		srv.sendError(w, fmt.Sprintf("start claude: %v", err))
		return
	}

	go pipeStderr(stderr, s.id)
	go srv.streamEvents(ctx, s, stdout)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

func (srv *server) streamEvents(ctx context.Context, s *session, stdout io.Reader) {
	defer close(s.events)
	defer close(s.done)

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return
		default:
		}

		line := scanner.Text()
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}

		parsed := parseStreamEvent(ev)
		if parsed != "" {
			data, _ := json.Marshal(ev)
			select {
			case s.events <- event{Type: parsed, Data: data}:
			case <-ctx.Done():
				return
			}
		}

		if typ, _ := ev["type"].(string); typ == "result" {
			data, _ := json.Marshal(ev)
			select {
			case s.events <- event{Type: "result", Data: data}:
			case <-ctx.Done():
				return
			}
		}
	}

	if err := scanner.Err(); err != nil {
		data, _ := json.Marshal(map[string]string{"error": err.Error()})
		select {
		case s.events <- event{Type: "error", Data: data}:
		default:
		}
	}
}

func parseStreamEvent(ev map[string]any) string {
	typ, _ := ev["type"].(string)
	switch typ {
	case "stream_event":
		if event, ok := ev["event"].(map[string]any); ok {
			if delta, ok := event["delta"].(map[string]any); ok {
				switch delta["type"] {
				case "text_delta":
					return "text_delta"
				case "input_json_delta":
					return "input_json_delta"
				}
			}
			if block, ok := event["content_block"].(map[string]any); ok {
				if block["type"] == "tool_use" {
					return "tool_use"
				}
			}
		}
		return "stream_event"
	case "system":
		sub, _ := ev["subtype"].(string)
		if sub == "init" {
			return "system_init"
		}
		if sub == "api_retry" {
			return "api_retry"
		}
		return "system"
	case "user":
		return "user_message"
	case "assistant":
		return "assistant_message"
	}
	return ""
}

func (srv *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session_id")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	var s *session
	if sid != "" {
		srv.mu.Lock()
		s = srv.sessions[sid]
		srv.mu.Unlock()

		if s == nil {
			io.WriteString(w, formatSSE("error", `{"error":"session not found"}`))
			flusher.Flush()
			return
		}
	}

	srv.mu.Lock()
	if s == nil {
		for _, v := range srv.sessions {
			s = v
			break
		}
	}
	srv.mu.Unlock()

	if s == nil {
		io.WriteString(w, formatSSE("error", `{"error":"no sessions"}`))
		flusher.Flush()
		return
	}

	slog.Info("SSE client connected", "session", s.id)

	for {
		select {
		case <-r.Context().Done():
			slog.Info("SSE client disconnected", "session", s.id)
			return
		case ev, ok := <-s.events:
			if !ok {
				io.WriteString(w, formatSSE("done", `{}`))
				flusher.Flush()
				slog.Info("SSE stream ended", "session", s.id)
				return
			}
			io.WriteString(w, formatSSE(ev.Type, string(ev.Data)))
			flusher.Flush()
		}
	}
}

func (srv *server) handleAbort(w http.ResponseWriter, r *http.Request, s *session) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd == nil || s.cmd.Process == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "nothing-to-abort"})
		return
	}

	slog.Info("aborting session", "id", s.id)

	if err := s.cmd.Process.Signal(syscall.SIGINT); err == nil {
		time.Sleep(2 * time.Second)
	}

	if s.cancel != nil {
		s.cancel()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "aborted"})
}

func (srv *server) handleExport(w http.ResponseWriter, r *http.Request, s *session) {
	s.mu.Lock()
	id := s.id
	model := s.model
	s.mu.Unlock()

	export, err := readSessionExport(id, model)
	if err != nil {
		slog.Warn("export failed", "session", id, "err", err)
		http.Error(w, fmt.Sprintf("export failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, export)
}

func readSessionExport(sessionID, model string) (string, error) {
	entries, err := os.ReadDir("/workspace/.claude/projects")
	if err != nil {
		return "", fmt.Errorf("read projects dir: %w", err)
	}

	var latestDir string
	var latestTime time.Time

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(latestTime) {
			latestTime = info.ModTime()
			latestDir = entry.Name()
		}
	}

	if latestDir == "" {
		return "", fmt.Errorf("no Claude project directories found")
	}

	projectPath := "/workspace/.claude/projects/" + latestDir
	jsonlDir := projectPath

	subEntries, err := os.ReadDir(projectPath)
	if err != nil {
		return "", fmt.Errorf("read project dir: %w", err)
	}

	for _, sub := range subEntries {
		if sub.IsDir() {
			jsonlDir = projectPath + "/" + sub.Name()
			break
		}
	}

	jsonlFiles, err := filepathGlob(jsonlDir + "/*.jsonl")
	if err != nil || len(jsonlFiles) == 0 {
		return "", fmt.Errorf("no session files found in %s", jsonlDir)
	}

	latestFile := jsonlFiles[len(jsonlFiles)-1]
	return renderSessionMarkdown(latestFile, model)
}

func filepathGlob(pattern string) ([]string, error) {
	dir := pattern[:strings.LastIndex(pattern, "/")]
	prefix := pattern[strings.LastIndex(pattern, "/")+1:]
	if !strings.Contains(prefix, "*") {
		if _, err := os.Stat(pattern); err == nil {
			return []string{pattern}, nil
		}
		return nil, fmt.Errorf("file not found: %s", pattern)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var matches []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".jsonl") {
			matches = append(matches, dir+"/"+e.Name())
		}
	}
	return matches, nil
}

func renderSessionMarkdown(path, model string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Claude Session\n\nModel: %s\n\n", model)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}

		typ, _ := ev["type"].(string)
		switch typ {
		case "assistant":
			content, _ := ev["message"].(map[string]any)
			if content != nil {
				if text, ok := content["text"].(string); ok && text != "" {
					sb.WriteString(text)
					sb.WriteString("\n\n")
				}
			}
		case "user":
			msg, _ := ev["message"].(map[string]any)
			if msg != nil {
				if text, ok := msg["text"].(string); ok && text != "" {
					fmt.Fprintf(&sb, "> %s\n\n", text)
				}
			}
		case "system":
			sub, _ := ev["subtype"].(string)
			if sub == "tool_result" || sub == "tool_use" {
				data, _ := json.MarshalIndent(ev, "", "  ")
				fmt.Fprintf(&sb, "```json\n%s\n```\n\n", string(data))
			}
		}
	}

	return sb.String(), scanner.Err()
}

func (srv *server) sendError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func pipeStderr(reader io.Reader, sessionID string) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 4096 {
			line = line[:4096] + "..."
		}
		slog.Info("claude stderr", "session", sessionID, "line", line)
	}
}

func resolveAgentFile(agentName string) string {
	paths := []string{
		"/workspace/.claude/agents/" + agentName + ".md",
		"/workspace/.opencode/agent/" + agentName + ".md",
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err == nil {
			return string(data)
		}
	}
	return ""
}

func generateSessionID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func formatSSE(eventType, data string) string {
	var sb strings.Builder
	if eventType != "" {
		fmt.Fprintf(&sb, "event: %s\n", eventType)
	}
	for _, line := range strings.Split(data, "\n") {
		fmt.Fprintf(&sb, "data: %s\n", line)
	}
	sb.WriteString("\n")
	return sb.String()
}
