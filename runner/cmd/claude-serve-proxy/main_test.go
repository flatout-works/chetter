package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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
