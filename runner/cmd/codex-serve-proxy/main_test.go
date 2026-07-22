package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestTokenUsageDelta(t *testing.T) {
	s := &session{}
	first, ok := tokenUsageDelta(s, json.RawMessage(`{"tokenUsage":{"total":{"inputTokens":10,"cachedInputTokens":4,"outputTokens":3,"reasoningOutputTokens":2}}}`))
	if !ok || first.InputTokens != 10 || first.CacheReadTokens != 4 || first.OutputTokens != 3 || first.ReasoningTokens != 2 {
		t.Fatalf("first usage = %+v, ok=%v", first, ok)
	}
	second, ok := tokenUsageDelta(s, json.RawMessage(`{"tokenUsage":{"total":{"inputTokens":15,"cachedInputTokens":5,"outputTokens":8,"reasoningOutputTokens":3}}}`))
	if !ok || second.InputTokens != 5 || second.CacheReadTokens != 1 || second.OutputTokens != 5 || second.ReasoningTokens != 1 {
		t.Fatalf("second usage = %+v, ok=%v", second, ok)
	}
}

func TestCompleteTurnWritesExportBeforeCompletion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	a := &appServer{}
	s := &session{id: "thread-1", turnID: "turn-1", prompt: "work", done: make(chan struct{}), events: make(chan sseEvent, 1)}
	s.summary.WriteString("finished")
	a.completeTurn(s, json.RawMessage(`{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}`))
	select {
	case <-s.done:
	case <-time.After(time.Second):
		t.Fatal("turn did not complete")
	}
	data, err := os.ReadFile(sessionExportPathForHome(home, s.id))
	if err != nil || !strings.Contains(string(data), "finished") {
		t.Fatalf("export = %q, %v", data, err)
	}
}

func TestAppServerEOFFailsPendingCallsAndSessions(t *testing.T) {
	responseCh := make(chan rpcMessage, 1)
	s := &session{id: "thread-1", done: make(chan struct{}), events: make(chan sseEvent, 1)}
	a := &appServer{
		pending:  map[string]chan rpcMessage{"1": responseCh},
		sessions: map[string]*session{s.id: s},
	}
	a.readLoop(bytes.NewReader(nil))
	select {
	case response := <-responseCh:
		if response.Error == nil || !strings.Contains(response.Error.Message, "exited") {
			t.Fatalf("pending response = %+v", response)
		}
	default:
		t.Fatal("pending call was not failed")
	}
	select {
	case <-s.done:
		if _, err := s.result(); err == nil || !strings.Contains(err.Error(), "exited") {
			t.Fatalf("session error = %v", err)
		}
	default:
		t.Fatal("active session was not failed")
	}
}

func TestTerminalEventDeliveredWithFullEventChannel(t *testing.T) {
	s := &session{id: "thread-1", done: make(chan struct{}), events: make(chan sseEvent, 1)}
	s.events <- sseEvent{Type: "codex.delta", Data: "buffered"}
	close(s.done)
	a := &appServer{first: s, firstCh: make(chan struct{})}
	srv := &server{app: a}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/event", nil)
	srv.handleEvents(recorder, request)
	if !strings.Contains(recorder.Body.String(), "event: done") || !strings.Contains(recorder.Body.String(), `"status":"completed"`) {
		t.Fatalf("terminal SSE missing: %q", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "buffered") {
		t.Fatalf("queued event was not drained: %q", recorder.Body.String())
	}
}

func TestTerminalEventReportsFailure(t *testing.T) {
	s := &session{id: "thread-1", runErr: "app server exited", done: make(chan struct{}), events: make(chan sseEvent, 1)}
	close(s.done)
	a := &appServer{first: s, firstCh: make(chan struct{})}
	srv := &server{app: a}
	recorder := httptest.NewRecorder()
	srv.handleEvents(recorder, httptest.NewRequest("GET", "/event", nil))
	if !strings.Contains(recorder.Body.String(), `"status":"failed"`) || !strings.Contains(recorder.Body.String(), "app server exited") {
		t.Fatalf("terminal failure SSE = %q", recorder.Body.String())
	}
}

func TestCallReturnsAppServerEOF(t *testing.T) {
	stdinReader, stdinWriter := io.Pipe()
	a := &appServer{stdin: stdinWriter, pending: make(map[string]chan rpcMessage), sessions: make(map[string]*session)}
	go func() {
		_, _ = bufio.NewReader(stdinReader).ReadBytes('\n')
		a.fail(io.EOF)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := a.call(ctx, "turn/start", map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("call error = %v", err)
	}
}

func TestItemDetail(t *testing.T) {
	if got := itemDetail(json.RawMessage(`{"item":{"type":"commandExecution","command":"go test ./..."}}`)); got != "command: go test ./..." {
		t.Fatalf("command detail = %q", got)
	}
	if got := itemDetail(json.RawMessage(`{"item":{"type":"mcpToolCall","tool":"create_pr"}}`)); got != "MCP tool: create_pr" {
		t.Fatalf("MCP detail = %q", got)
	}
}

func TestSessionExportPathUsesCodexHome(t *testing.T) {
	t.Setenv("CODEX_HOME", "/tmp/codex")
	if got := sessionExportPath("thread_1"); got != "/tmp/codex/session-thread_1.md" {
		t.Fatalf("path = %q", got)
	}
}

func TestBasicAuthHeader(t *testing.T) {
	if got := basicAuthHeader("secret"); !strings.HasPrefix(got, "Basic ") {
		t.Fatalf("header = %q", got)
	}
}
