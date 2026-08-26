package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestWithAuthAcceptsRunnerSecret(t *testing.T) {
	srv := &server{password: "secret"}
	handler := withAuth(srv, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.Header.Set("Authorization", basicAuthHeader("secret"))
	rr := httptest.NewRecorder()

	handler(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestProgressEventPayloadUsesNestedStreamEvent(t *testing.T) {
	payload, err := json.Marshal(progressEventPayload(map[string]any{
		"type": "stream_event",
		"event": map[string]any{
			"content_block": map[string]any{
				"name": "Bash",
			},
		},
	}))
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if string(payload) != `{"content_block":{"name":"Bash"}}` {
		t.Fatalf("payload = %s", payload)
	}
}

func TestWithAuthRejectsWrongSecret(t *testing.T) {
	srv := &server{password: "secret"}
	handler := withAuth(srv, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/config", nil)
	req.Header.Set("Authorization", basicAuthHeader("wrong"))
	rr := httptest.NewRecorder()

	handler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestSessionRecordsTextDeltaAndResultError(t *testing.T) {
	s := &session{}
	s.recordStreamEvent(map[string]any{
		"type": "stream_event",
		"event": map[string]any{
			"delta": map[string]any{
				"type": "text_delta",
				"text": "hello",
			},
		},
	})
	s.recordStreamEvent(map[string]any{
		"type":  "result",
		"error": "boom",
	})

	if got := s.summary.String(); got != "hello" {
		t.Fatalf("summary = %q, want hello", got)
	}
	if !strings.Contains(s.runErr, "boom") {
		t.Fatalf("runErr = %q, want boom", s.runErr)
	}
}

func TestSessionRejectsNotLoggedInResult(t *testing.T) {
	s := &session{}
	s.recordStreamEvent(map[string]any{
		"type":   "result",
		"result": "Not logged in · Please run /login",
	})
	if !strings.Contains(s.runErr, "not logged in") {
		t.Fatalf("runErr = %q, want login failure", s.runErr)
	}

	s = &session{}
	s.recordStreamEvent(map[string]any{
		"type":   "result",
		"result": "genuine assistant result",
	})
	if s.runErr != "" {
		t.Fatalf("runErr = %q, want empty for genuine result", s.runErr)
	}
}

func TestPromptPreservesFlaggedResultError(t *testing.T) {
	srv, s := testProxyServer(t, `printf '%s\n' '{"type":"result","subtype":"success","is_error":true,"result":"API Error: Request rejected (429): subscription rate limits exceeded"}'`)
	rr := sendTestPrompt(srv, s.id, `{"prompt":"test"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "API Error: Request rejected (429)") {
		t.Fatalf("body = %q, want provider error", rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "reported success") {
		t.Fatalf("body = %q, contains misleading success fallback", rr.Body.String())
	}
}

func TestSessionUsesRetryDetailsForEmptyResultError(t *testing.T) {
	s := &session{}
	s.recordStreamEvent(map[string]any{
		"type":           "system",
		"subtype":        "api_retry",
		"attempt":        float64(3),
		"max_retries":    float64(3),
		"retry_delay_ms": float64(1500),
		"error_status":   float64(429),
		"error":          "rate_limit",
	})
	s.recordStreamEvent(map[string]any{
		"type":     "result",
		"subtype":  "success",
		"is_error": true,
	})

	want := "Claude provider request failed after API retry: rate_limit, HTTP 429, attempt 3/3"
	if s.runErr != want {
		t.Fatalf("runErr = %q, want %q", s.runErr, want)
	}
}

func TestSessionRejectsMCPInitializationErrors(t *testing.T) {
	s := &session{}
	s.recordStreamEvent(map[string]any{
		"type":    "system",
		"subtype": "init",
		"mcp_server_errors": []any{
			map[string]any{
				"name":    "runner-bridge",
				"type":    "invalid_config",
				"message": "missing URL",
			},
		},
	})

	for _, want := range []string{"Claude MCP initialization failed", `"runner-bridge"`, "invalid_config", "missing URL"} {
		if !strings.Contains(s.runErr, want) {
			t.Fatalf("runErr = %q, want substring %q", s.runErr, want)
		}
	}
}

func TestSessionBoundsResultErrors(t *testing.T) {
	s := &session{}
	s.recordStreamEvent(map[string]any{
		"type":     "result",
		"subtype":  "success",
		"is_error": true,
		"result":   strings.Repeat("x", maxClaudeErrorBytes+100),
	})
	if len(s.runErr) != maxClaudeErrorBytes+3 || !strings.HasSuffix(s.runErr, "...") {
		t.Fatalf("bounded error length = %d, suffix=%q", len(s.runErr), s.runErr[len(s.runErr)-3:])
	}
}

func TestSessionParsesResultErrorList(t *testing.T) {
	s := &session{}
	s.recordStreamEvent(map[string]any{
		"type":     "result",
		"subtype":  "error_max_turns",
		"is_error": true,
		"errors":   []any{"Turn limit reached", "Ran out of turns"},
	})
	want := "error_max_turns: Turn limit reached; Ran out of turns"
	if s.runErr != want {
		t.Fatalf("runErr = %q, want %q", s.runErr, want)
	}

	s = &session{}
	s.recordStreamEvent(map[string]any{
		"type":     "result",
		"subtype":  "error_max_budget_usd",
		"is_error": true,
		"errors":   []any{"Budget limit reached"},
	})
	if !strings.Contains(s.runErr, "error_max_budget_usd") || !strings.Contains(s.runErr, "Budget limit reached") {
		t.Fatalf("runErr = %q, want subtype and message", s.runErr)
	}

	s = &session{}
	s.recordStreamEvent(map[string]any{
		"type":     "result",
		"subtype":  "success",
		"is_error": true,
		"errors":   []any{""}, // whitespace-only entries are dropped
		"result":   "API Error: Request rejected (429)",
	})
	if s.runErr != "API Error: Request rejected (429)" {
		t.Fatalf("runErr = %q, want result fallback after empty errors", s.runErr)
	}
}

func TestMaxTurns(t *testing.T) {
	if got := maxTurns(); got != defaultMaxTurns {
		t.Fatalf("maxTurns() = %d, want %d", got, defaultMaxTurns)
	}
	t.Setenv("CHETTER_CLAUDE_MAX_TURNS", "1234")
	if got := maxTurns(); got != 1234 {
		t.Fatalf("maxTurns() = %d, want 1234", got)
	}
	for _, invalid := range []string{"", " ", "abc", "0", "-5"} {
		t.Setenv("CHETTER_CLAUDE_MAX_TURNS", invalid)
		if got := maxTurns(); got != defaultMaxTurns {
			t.Fatalf("maxTurns() with %q = %d, want %d", invalid, got, defaultMaxTurns)
		}
	}
}

func TestPromptArgsHeadlessHardening(t *testing.T) {
	argsSeen := make(chan []string, 1)
	srv := newTestProxyServer(t.TempDir(), `printf '%s\n' '{"type":"result","subtype":"success","result":"done"}'`)
	srv.command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		select {
		case argsSeen <- append([]string(nil), args...):
		default:
		}
		return exec.CommandContext(ctx, "sh", "-c", `printf '%s\n' '{"type":"result","subtype":"success","result":"done"}'`)
	}
	s := newSession("proxy-session")
	srv.sessions[s.id] = s

	rr := sendTestPrompt(srv, s.id, `{"prompt":"test"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
	}
	args := <-argsSeen
	if !containsArgs(args, "--permission-mode", "dontAsk") {
		t.Fatalf("args missing --permission-mode dontAsk: %q", args)
	}
	if !containsArgs(args, "--add-dir", "/tmp") {
		t.Fatalf("args missing --add-dir /tmp: %q", args)
	}
	if !containsArgs(args, "--max-turns", strconv.Itoa(defaultMaxTurns)) {
		t.Fatalf("args missing --max-turns %d: %q", defaultMaxTurns, args)
	}
	if containsArgs(args, "--strict-mcp-config") {
		t.Fatalf("args must not include --strict-mcp-config without a config file: %q", args)
	}

	// With a runner-generated MCP config present, strict loading kicks in.
	strictPath := filepath.Join(srv.workspaceDir(), ".claude", "chetter-mcp.json")
	if err := os.MkdirAll(filepath.Dir(strictPath), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strictPath, []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	argsSeen = make(chan []string, 1)
	srv.command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		select {
		case argsSeen <- append([]string(nil), args...):
		default:
		}
		return exec.CommandContext(ctx, "sh", "-c", `printf '%s\n' '{"type":"result","subtype":"success","result":"done"}'`)
	}
	s2 := newSession("proxy-session-2")
	srv.sessions[s2.id] = s2
	rr = sendTestPrompt(srv, s2.id, `{"prompt":"test"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("strict status = %d; body=%s", rr.Code, rr.Body.String())
	}
	args = <-argsSeen
	if !containsArgs(args, "--mcp-config", strictPath) || !containsArgs(args, "--strict-mcp-config") {
		t.Fatalf("args missing strict mcp config: %q", args)
	}
}

func TestPromptReportsNonzeroChildExit(t *testing.T) {
	srv, s := testProxyServer(t, "exit 7")
	rr := sendTestPrompt(srv, s.id, `{"prompt":"test"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "exit status 7") {
		t.Fatalf("body = %q, want child exit status", rr.Body.String())
	}
}

func TestPromptRejectsMissingResultEvent(t *testing.T) {
	srv, s := testProxyServer(t, `printf '%s\n' 'not-json'`)
	rr := sendTestPrompt(srv, s.id, `{"prompt":"test"}`)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rr.Code, http.StatusInternalServerError, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Claude exited without a result event") {
		t.Fatalf("body = %q, want missing result error", rr.Body.String())
	}
}

func TestPromptRejectsDuplicateRun(t *testing.T) {
	srv, s := testProxyServer(t, "exec sleep 30")
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		firstDone <- sendTestPrompt(srv, s.id, `{"prompt":"first"}`)
	}()
	waitForCommand(t, s)

	duplicate := sendTestPrompt(srv, s.id, `{"prompt":"second"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d, want %d", duplicate.Code, http.StatusConflict)
	}

	abort := httptest.NewRecorder()
	srv.handleAbort(abort, httptest.NewRequest(http.MethodPost, "/", nil), s)
	if abort.Code != http.StatusOK {
		t.Fatalf("abort status = %d, want %d", abort.Code, http.StatusOK)
	}
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first prompt did not stop after abort")
	}
}

func TestAbortSignalsRunningChild(t *testing.T) {
	srv, s := testProxyServer(t, "exec sleep 30")
	promptDone := make(chan struct{})
	go func() {
		sendTestPrompt(srv, s.id, `{"prompt":"test"}`)
		close(promptDone)
	}()
	waitForCommand(t, s)

	rr := httptest.NewRecorder()
	srv.handleAbort(rr, httptest.NewRequest(http.MethodPost, "/", nil), s)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "aborted") {
		t.Fatalf("abort response = %d %q", rr.Code, rr.Body.String())
	}
	select {
	case <-promptDone:
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not stop after abort")
	}
}

func TestResumeRestoresProxySessionAndUsesNativeID(t *testing.T) {
	workspace := t.TempDir()
	const proxyID = "proxy-session"
	first := newTestProxyServer(workspace, `printf '%s\n' '{"type":"system","subtype":"init","session_id":"native-session"}' '{"type":"result","subtype":"success","result":"done"}'`)
	first.sessions[proxyID] = newSession(proxyID)
	rr := sendTestPrompt(first, proxyID, `{"prompt":"initial"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("initial status = %d; body=%s", rr.Code, rr.Body.String())
	}

	mapping, err := first.readSessionMapping(proxyID)
	if err != nil {
		t.Fatalf("read persisted mapping: %v", err)
	}
	if mapping.NativeSessionID != "native-session" {
		t.Fatalf("native ID = %q, want native-session", mapping.NativeSessionID)
	}

	argsSeen := make(chan []string, 1)
	resumed := newTestProxyServer(workspace, `printf '%s\n' '{"type":"result","subtype":"success","result":"resumed"}'`)
	resumed.command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		argsSeen <- append([]string(nil), args...)
		return exec.CommandContext(ctx, "sh", "-c", resumedScript())
	}
	rr = sendTestPrompt(resumed, proxyID, `{"prompt":"follow up","resume_session_id":"proxy-session"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("resume status = %d; body=%s", rr.Code, rr.Body.String())
	}
	args := <-argsSeen
	if !containsArgs(args, "--resume", "native-session") {
		t.Fatalf("resume args = %q, want native Claude session ID", args)
	}
}

func TestResumeDiscoversSingleLegacyTranscript(t *testing.T) {
	workspace := t.TempDir()
	project := filepath.Join(workspace, ".claude", "projects", "legacy-project")
	if err := os.MkdirAll(project, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "native-legacy.jsonl"), []byte("{}\n"), 0640); err != nil {
		t.Fatal(err)
	}

	srv := newTestProxyServer(workspace, resumedScript())
	s, err := srv.lookupSession("old-proxy-id")
	if err != nil {
		t.Fatalf("lookup legacy session: %v", err)
	}
	if s.nativeID != "native-legacy" {
		t.Fatalf("native ID = %q, want native-legacy", s.nativeID)
	}
	mapping, err := srv.readSessionMapping("old-proxy-id")
	if err != nil || mapping.NativeSessionID != "native-legacy" {
		t.Fatalf("persisted mapping = %#v, %v", mapping, err)
	}
}

func TestResumeRejectsAmbiguousLegacyTranscripts(t *testing.T) {
	workspace := t.TempDir()
	project := filepath.Join(workspace, ".claude", "projects", "legacy-project")
	if err := os.MkdirAll(project, 0750); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"native-a.jsonl", "native-b.jsonl"} {
		if err := os.WriteFile(filepath.Join(project, name), []byte("{}\n"), 0640); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := newTestProxyServer(workspace, resumedScript()).lookupSession("old-proxy-id"); err == nil || !strings.Contains(err.Error(), "found 2") {
		t.Fatalf("ambiguous lookup error = %v", err)
	}
}

func TestStreamEmitsCompletedOnlyAfterSuccessfulExit(t *testing.T) {
	srv, s := testProxyServer(t, `printf '%s\n' '{"type":"result","subtype":"success","result":"done","usage":{"input_tokens":3}}'`)
	rr := sendTestPrompt(srv, s.id, `{"prompt":"test"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var types []string
	for ev := range s.events {
		types = append(types, ev.Type)
	}
	if strings.Join(types, ",") != "result,completed" {
		t.Fatalf("event types = %v, want result then completed", types)
	}
}

func TestReadSessionExportSelectsNativeSession(t *testing.T) {
	workspace := t.TempDir()
	project := filepath.Join(workspace, ".claude", "projects", "project")
	if err := os.MkdirAll(project, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "native-a.jsonl"), []byte(`{"type":"assistant","message":{"text":"wrong"}}`+"\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "native-b.jsonl"), []byte(`{"type":"assistant","message":{"text":"right"}}`+"\n"), 0640); err != nil {
		t.Fatal(err)
	}
	export, err := readSessionExport(workspace, "native-b", "model")
	if err != nil {
		t.Fatalf("read export: %v", err)
	}
	if !strings.Contains(export, "right") || strings.Contains(export, "wrong") {
		t.Fatalf("export selected wrong transcript: %q", export)
	}
}

func TestPromptAppendsAgentSystemPromptInsteadOfReplacing(t *testing.T) {
	workspace := t.TempDir()
	agentDir := filepath.Join(workspace, ".claude", "agents")
	if err := os.MkdirAll(agentDir, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "triage.md"), []byte("persona"), 0644); err != nil {
		t.Fatal(err)
	}

	argsSeen := make(chan []string, 1)
	srv := newTestProxyServer(workspace, `printf '%s\n' '{"type":"result","subtype":"success","result":"done"}'`)
	srv.command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		select {
		case argsSeen <- append([]string(nil), args...):
		default:
		}
		return exec.CommandContext(ctx, "sh", "-c", `printf '%s\n' '{"type":"result","subtype":"success","result":"done"}'`)
	}
	s := newSession("proxy-session")
	srv.sessions[s.id] = s

	rr := sendTestPrompt(srv, s.id, `{"prompt":"test","agent":"triage"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
	}
	args := <-argsSeen
	if !containsArgs(args, "--append-system-prompt-file", filepath.Join(agentDir, "triage.md")) {
		t.Fatalf("args missing --append-system-prompt-file: %q", args)
	}
	for i := 0; i+len("--system-prompt") <= len(args); i++ {
		if args[i] == "--system-prompt" {
			t.Fatalf("args must not replace the system prompt: %q", args)
		}
	}
}

func TestSessionStatusReportsBusyWhileRunning(t *testing.T) {
	srv, s := testProxyServer(t, "exec sleep 30")
	promptDone := make(chan struct{})
	go func() {
		sendTestPrompt(srv, s.id, `{"prompt":"test"}`)
		close(promptDone)
	}()
	waitForCommand(t, s)

	rr := httptest.NewRecorder()
	srv.handleStatus(rr, httptest.NewRequest(http.MethodGet, "/", nil), s)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"busy"`) {
		t.Fatalf("status while running = %d %q", rr.Code, rr.Body.String())
	}

	abort := httptest.NewRecorder()
	srv.handleAbort(abort, httptest.NewRequest(http.MethodPost, "/", nil), s)
	if abort.Code != http.StatusOK {
		t.Fatalf("abort status = %d", abort.Code)
	}
	select {
	case <-promptDone:
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not stop after abort")
	}

	rr = httptest.NewRecorder()
	srv.handleStatus(rr, httptest.NewRequest(http.MethodGet, "/", nil), s)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"idle"`) {
		t.Fatalf("status after completion = %d %q", rr.Code, rr.Body.String())
	}
}

func TestContinueOnIdleSessionResumesNativeID(t *testing.T) {
	workspace := t.TempDir()
	const proxyID = "proxy-session"
	native := filepath.Join(workspace, ".claude", "chetter-sessions", proxyID+".json")
	if err := os.MkdirAll(filepath.Dir(native), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte(`{"native_session_id":"native-42"}`), 0600); err != nil {
		t.Fatal(err)
	}

	argsSeen := make(chan []string, 1)
	srv := newTestProxyServer(workspace, `printf '%s\n' '{"type":"result","subtype":"success","result":"resumed"}'`)
	srv.command = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		select {
		case argsSeen <- append([]string(nil), args...):
		default:
		}
		return exec.CommandContext(ctx, "sh", "-c", `printf '%s\n' '{"type":"result","subtype":"success","result":"resumed"}'`)
	}

	// Continue on an unknown session ID must 404 (no mapping, no live session).
	rr := sendTestContinue(srv, "missing-session")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown session continue status = %d, want 404", rr.Code)
	}

	if _, err := srv.lookupSession(proxyID); err != nil {
		t.Fatalf("lookup session: %v", err)
	}
	rr = sendTestContinue(srv, proxyID)
	if rr.Code != http.StatusOK {
		t.Fatalf("continue status = %d; body=%s", rr.Code, rr.Body.String())
	}
	args := <-argsSeen
	if !containsArgs(args, "--resume", "native-42") {
		t.Fatalf("continue args missing --resume native-42: %q", args)
	}
}

func TestContinueOnBusySessionConflicts(t *testing.T) {
	srv, s := testProxyServer(t, "exec sleep 30")
	promptDone := make(chan struct{})
	go func() {
		sendTestPrompt(srv, s.id, `{"prompt":"first"}`)
		close(promptDone)
	}()
	waitForCommand(t, s)

	rr := sendTestContinue(srv, s.id)
	if rr.Code != http.StatusConflict {
		t.Fatalf("busy continue status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}

	abort := httptest.NewRecorder()
	srv.handleAbort(abort, httptest.NewRequest(http.MethodPost, "/", nil), s)
	if abort.Code != http.StatusOK {
		t.Fatalf("abort status = %d", abort.Code)
	}
	select {
	case <-promptDone:
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not stop after abort")
	}
}

func sendTestContinue(srv *server, sessionID string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/session/"+sessionID+"/continue", strings.NewReader(`{"prompt":"continue"}`))
	srv.handleSession(rr, req)
	return rr
}

func testProxyServer(t *testing.T, script string) (*server, *session) {
	t.Helper()
	srv := newTestProxyServer(t.TempDir(), script)
	s := newSession("proxy-session")
	srv.sessions[s.id] = s
	return srv, s
}

func newTestProxyServer(workspace, script string) *server {
	return &server{
		sessions:   make(map[string]*session),
		workspace:  workspace,
		abortGrace: 10 * time.Millisecond,
		command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", script)
		},
	}
}

func resumedScript() string {
	return `printf '%s\n' '{"type":"result","subtype":"success","result":"resumed"}'`
}

func sendTestPrompt(srv *server, sessionID, body string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/session/"+sessionID+"/message", strings.NewReader(body))
	srv.handleSession(rr, req)
	return rr
}

func waitForCommand(t *testing.T, s *session) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		running := s.cmd != nil && s.cmd.Process != nil
		s.mu.Unlock()
		if running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("command did not start")
}

func containsArgs(args []string, values ...string) bool {
	for i := 0; i+len(values) <= len(args); i++ {
		if strings.Join(args[i:i+len(values)], "\x00") == strings.Join(values, "\x00") {
			return true
		}
	}
	return false
}
