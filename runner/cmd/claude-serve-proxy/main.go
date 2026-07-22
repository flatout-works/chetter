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
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

type session struct {
	mu       sync.Mutex
	id       string
	cmd      *exec.Cmd
	cancel   context.CancelFunc
	events   chan event
	done     chan struct{}
	prompt   string
	model    string
	resumeID string
	summary  strings.Builder
	runErr   string
	nativeID string
	result   json.RawMessage
	complete bool
}

type event struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type server struct {
	mu         sync.Mutex
	sessions   map[string]*session
	password   string
	workspace  string
	command    func(context.Context, string, ...string) *exec.Cmd
	abortGrace time.Duration
}

type sessionMapping struct {
	NativeSessionID string `json:"native_session_id"`
	Model           string `json:"model,omitempty"`
}

func main() {
	port := flag.Int("port", 9999, "HTTP server port")
	flag.Parse()

	password := os.Getenv("CLAUDE_SERVE_PROXY_TOKEN")
	if password == "" {
		password = generatePassword()
	}

	srv := &server{
		sessions:   make(map[string]*session),
		password:   password,
		workspace:  "/workspace",
		command:    exec.CommandContext,
		abortGrace: 2 * time.Second,
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
		s.mu.Lock()
		cancel := s.cancel
		s.mu.Unlock()
		if cancel != nil {
			cancel()
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

func newSession(id string) *session {
	return &session{
		id:     id,
		events: make(chan event, 1024),
		done:   make(chan struct{}),
	}
}

func (srv *server) workspaceDir() string {
	if srv.workspace != "" {
		return srv.workspace
	}
	return "/workspace"
}

func (srv *server) lookupSession(id string) (*session, error) {
	srv.mu.Lock()
	s := srv.sessions[id]
	srv.mu.Unlock()
	if s != nil {
		return s, nil
	}

	mapping, err := srv.readSessionMapping(id)
	if err != nil {
		return nil, err
	}
	s = newSession(id)
	s.nativeID = mapping.NativeSessionID
	s.model = mapping.Model

	srv.mu.Lock()
	if existing := srv.sessions[id]; existing != nil {
		s = existing
	} else {
		srv.sessions[id] = s
	}
	srv.mu.Unlock()
	return s, nil
}

func (srv *server) mappingPath(id string) (string, error) {
	if id == "" || filepath.Base(id) != id {
		return "", fmt.Errorf("invalid session ID")
	}
	return filepath.Join(srv.workspaceDir(), ".claude", "chetter-sessions", id+".json"), nil
}

func (srv *server) readSessionMapping(id string) (sessionMapping, error) {
	path, err := srv.mappingPath(id)
	if err != nil {
		return sessionMapping{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return sessionMapping{}, err
		}
		nativeID, discoverErr := srv.discoverNativeSessionID()
		if discoverErr != nil {
			return sessionMapping{}, discoverErr
		}
		mapping := sessionMapping{NativeSessionID: nativeID}
		stub := newSession(id)
		srv.recordNativeSession(stub, nativeID)
		return mapping, nil
	}
	var mapping sessionMapping
	if err := json.Unmarshal(data, &mapping); err != nil {
		return sessionMapping{}, fmt.Errorf("decode session mapping: %w", err)
	}
	if mapping.NativeSessionID == "" {
		return sessionMapping{}, fmt.Errorf("session mapping has no native session ID")
	}
	return mapping, nil
}

func (srv *server) discoverNativeSessionID() (string, error) {
	projectsDir := filepath.Join(srv.workspaceDir(), ".claude", "projects")
	var ids []string
	err := filepath.WalkDir(projectsDir, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".jsonl" {
			ids = append(ids, strings.TrimSuffix(entry.Name(), ".jsonl"))
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("discover Claude session transcript: %w", err)
	}
	if len(ids) != 1 {
		return "", fmt.Errorf("cannot reconstruct Claude session: found %d native transcripts", len(ids))
	}
	return ids[0], nil
}

func (srv *server) recordNativeSession(s *session, nativeID string) {
	if nativeID == "" {
		return
	}
	s.mu.Lock()
	s.nativeID = nativeID
	mapping := sessionMapping{NativeSessionID: nativeID, Model: s.model}
	s.mu.Unlock()

	path, err := srv.mappingPath(s.id)
	if err != nil {
		slog.Warn("invalid Claude session mapping path", "session", s.id, "err", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		slog.Warn("create Claude session mapping directory", "session", s.id, "err", err)
		return
	}
	data, err := json.Marshal(mapping)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		slog.Warn("write Claude session mapping", "session", s.id, "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		slog.Warn("replace Claude session mapping", "session", s.id, "err", err)
	}
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
			if r.Header.Get("Authorization") != basicAuthHeader(srv.password) {
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
	s := newSession(id)

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

	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	s, err := srv.lookupSession(sid)
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
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
	Prompt   string   `json:"prompt"`
	Model    string   `json:"model"`
	Skills   []string `json:"skills"`
	Agent    string   `json:"agent"`
	ResumeID string   `json:"resume_session_id"`
}

func (srv *server) handleSendPrompt(w http.ResponseWriter, r *http.Request, s *session) {
	var req messageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())

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
	resumeID := req.ResumeID
	s.mu.Lock()
	if resumeID == s.id && s.nativeID != "" {
		resumeID = s.nativeID
	}
	s.mu.Unlock()
	if resumeID != "" {
		args = append(args, "--resume", resumeID)
	}

	slog.Info("starting claude", "session", s.id, "model", model, "resume", req.ResumeID)

	command := srv.command
	if command == nil {
		command = exec.CommandContext
	}
	cmd := command(ctx, args[0], args[1:]...)
	cmd.Dir = srv.workspaceDir()
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

	s.mu.Lock()
	if s.cmd != nil {
		s.mu.Unlock()
		cancel()
		http.Error(w, "session already running", http.StatusConflict)
		return
	}
	select {
	case <-s.done:
		s.mu.Unlock()
		cancel()
		http.Error(w, "session already finished", http.StatusConflict)
		return
	default:
	}
	s.prompt = req.Prompt
	s.model = model
	s.resumeID = resumeID
	s.cancel = cancel
	s.cmd = cmd
	if err := cmd.Start(); err != nil {
		cancel()
		s.cmd = nil
		s.cancel = nil
		s.mu.Unlock()
		srv.sendError(w, fmt.Sprintf("start claude: %v", err))
		return
	}
	s.mu.Unlock()

	go pipeStderr(stderr, s.id)
	go srv.streamEvents(ctx, s, stdout)

	select {
	case <-s.done:
	case <-r.Context().Done():
		s.mu.Lock()
		complete := s.complete
		s.mu.Unlock()
		if !complete {
			cancel()
		}
		return
	}

	s.mu.Lock()
	summary := s.summary.String()
	runErr := s.runErr
	s.mu.Unlock()

	if runErr != "" {
		srv.sendError(w, runErr)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "completed", "summary": summary})
}

func (srv *server) streamEvents(ctx context.Context, s *session, stdout io.Reader) {
	defer close(s.events)
	defer close(s.done)
	defer s.waitForCommand(ctx)

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
		s.recordStreamEvent(ev)
		if nativeID := nativeSessionID(ev); nativeID != "" {
			srv.recordNativeSession(s, nativeID)
		}
		if parsed != "" {
			data, _ := json.Marshal(progressEventPayload(ev))
			select {
			case s.events <- event{Type: parsed, Data: data}:
			case <-ctx.Done():
				return
			}
		}

		if typ, _ := ev["type"].(string); typ == "result" {
			data, _ := json.Marshal(ev)
			s.mu.Lock()
			s.result = append(s.result[:0], data...)
			s.mu.Unlock()
			select {
			case s.events <- event{Type: "result", Data: data}:
			case <-ctx.Done():
				return
			}
		}
	}

	if err := scanner.Err(); err != nil {
		s.setError(err.Error())
		data, _ := json.Marshal(map[string]string{"error": err.Error()})
		select {
		case s.events <- event{Type: "error", Data: data}:
		default:
		}
	}

	s.waitForCommand(ctx)
	s.mu.Lock()
	if ctx.Err() == nil && s.runErr == "" && len(s.result) > 0 {
		s.complete = true
		data := append(json.RawMessage(nil), s.result...)
		s.mu.Unlock()
		select {
		case s.events <- event{Type: "completed", Data: data}:
		case <-ctx.Done():
		}
		return
	}
	s.mu.Unlock()
}

func progressEventPayload(ev map[string]any) any {
	if typ, _ := ev["type"].(string); typ == "stream_event" {
		if nested, ok := ev["event"].(map[string]any); ok {
			return nested
		}
	}
	return ev
}

func (s *session) waitForCommand(ctx context.Context) {
	s.mu.Lock()
	cmd := s.cmd
	s.mu.Unlock()
	if cmd == nil {
		return
	}
	if err := cmd.Wait(); err != nil && ctx.Err() == nil {
		s.setError(err.Error())
	}
	s.mu.Lock()
	s.cmd = nil
	s.cancel = nil
	s.mu.Unlock()
}

func (s *session) recordStreamEvent(ev map[string]any) {
	typ, _ := ev["type"].(string)
	switch typ {
	case "assistant":
		if s.summary.Len() == 0 {
			appendAssistantMessage(&s.summary, ev)
		}
	case "stream_event":
		appendTextDelta(&s.summary, ev)
	case "result":
		if errText, _ := ev["error"].(string); errText != "" {
			s.setError(errText)
		}
		isError, _ := ev["is_error"].(bool)
		subtype, _ := ev["subtype"].(string)
		if isError || strings.HasPrefix(subtype, "error") {
			if message, _ := ev["message"].(string); message != "" {
				s.setError(message)
			} else if subtype != "" {
				s.setError("Claude result reported " + subtype)
			} else {
				s.setError("Claude result reported an error")
			}
		}
	}
}

func (s *session) setError(message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runErr == "" {
		s.runErr = message
	}
}

func appendAssistantMessage(sb *strings.Builder, ev map[string]any) {
	message, _ := ev["message"].(map[string]any)
	if message == nil {
		return
	}
	content, _ := message["content"].([]any)
	for _, item := range content {
		block, _ := item.(map[string]any)
		if block == nil || block["type"] != "text" {
			continue
		}
		if text, _ := block["text"].(string); text != "" {
			sb.WriteString(text)
		}
	}
}

func appendTextDelta(sb *strings.Builder, ev map[string]any) {
	event, _ := ev["event"].(map[string]any)
	if event == nil {
		return
	}
	delta, _ := event["delta"].(map[string]any)
	if delta == nil || delta["type"] != "text_delta" {
		return
	}
	if text, _ := delta["text"].(string); text != "" {
		sb.WriteString(text)
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

func nativeSessionID(ev map[string]any) string {
	id, _ := ev["session_id"].(string)
	if id != "" {
		return id
	}
	message, _ := ev["message"].(map[string]any)
	id, _ = message["session_id"].(string)
	return id
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
		var err error
		s, err = srv.lookupSession(sid)
		if err != nil {
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
	if s.cmd == nil || s.cmd.Process == nil {
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "nothing-to-abort"})
		return
	}
	cmd := s.cmd
	cancel := s.cancel
	done := s.done
	s.mu.Unlock()

	slog.Info("aborting session", "id", s.id)

	if err := cmd.Process.Signal(syscall.SIGINT); err == nil {
		grace := srv.abortGrace
		if grace <= 0 {
			grace = 2 * time.Second
		}
		select {
		case <-done:
		case <-time.After(grace):
		}
	}

	if cancel != nil {
		cancel()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "aborted"})
}

func (srv *server) handleExport(w http.ResponseWriter, r *http.Request, s *session) {
	s.mu.Lock()
	id := s.nativeID
	model := s.model
	s.mu.Unlock()

	export, err := readSessionExport(srv.workspaceDir(), id, model)
	if err != nil {
		slog.Warn("export failed", "session", id, "err", err)
		http.Error(w, fmt.Sprintf("export failed: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, export)
}

func readSessionExport(workspace, sessionID, model string) (string, error) {
	if sessionID == "" {
		return "", fmt.Errorf("claude native session ID is not available")
	}
	path, err := findSessionFile(filepath.Join(workspace, ".claude", "projects"), sessionID)
	if err != nil {
		return "", err
	}
	return renderSessionMarkdown(path, model)
}

func findSessionFile(projectsDir, sessionID string) (string, error) {
	if filepath.Base(sessionID) != sessionID {
		return "", fmt.Errorf("invalid Claude session ID")
	}
	want := sessionID + ".jsonl"
	var match string
	err := filepath.WalkDir(projectsDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && entry.Name() == want {
			if match != "" {
				return fmt.Errorf("multiple transcripts found for Claude session %s", sessionID)
			}
			match = path
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("find Claude session transcript: %w", err)
	}
	if match == "" {
		return "", fmt.Errorf("no transcript found for Claude session %s", sessionID)
	}
	return match, nil
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
