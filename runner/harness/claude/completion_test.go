package claude

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestWatchEventsSignalsCompletionAfterTerminalUsage(t *testing.T) {
	var order []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("session_id"); got != "proxy-id" {
			t.Errorf("session_id = %q, want proxy-id", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: result\ndata: {\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}\n\n")
		fmt.Fprint(w, "event: completed\ndata: {\"subtype\":\"success\",\"result\":\"finished\"}\n\n")
	}))
	defer server.Close()

	watchEvents(context.Background(), "task", server.URL, "", func(string, string) {}, func(usage task.TokenUsage) {
		if usage.InputTokens != 7 || usage.OutputTokens != 3 {
			t.Errorf("usage = %+v", usage)
		}
		order = append(order, "tokens")
	}, "proxy-id", func(summary string) {
		if summary != "finished" {
			t.Errorf("summary = %q, want finished", summary)
		}
		order = append(order, "completed")
	})

	if got := strings.Join(order, ","); got != "tokens,completed" {
		t.Fatalf("callback order = %q, want tokens,completed", got)
	}
}

func TestWatchEventsDoesNotTreatResultAsCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: result\ndata: {\"subtype\":\"error\",\"is_error\":true}\n\n")
		fmt.Fprint(w, "event: done\ndata: {}\n\n")
	}))
	defer server.Close()

	completed := false
	watchEvents(context.Background(), "task", server.URL, "", func(string, string) {}, nil, "proxy-id", func(string) {
		completed = true
	})
	if completed {
		t.Fatal("result event falsely signaled successful completion")
	}
}

func TestSendPromptRecoversFromLostResponseAfterTerminalEvent(t *testing.T) {
	eventWritten := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/event", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: result\ndata: {\"usage\":{\"input_tokens\":2}}\n\n")
		fmt.Fprint(w, "event: completed\ndata: {\"subtype\":\"success\",\"result\":\"terminal summary\"}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(eventWritten)
	})
	mux.HandleFunc("/session/proxy-id/message", func(w http.ResponseWriter, r *http.Request) {
		<-eventWritten
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("response writer does not support hijacking")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		conn.Close()
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	cc := New()
	idleCh := make(chan struct{})
	cc.SetCompletionContext("proxy-id", idleCh, func() { close(idleCh) })
	watchDone := make(chan struct{})
	go func() {
		cc.WatchEvents(context.Background(), "task", server.URL, "", func(string, string) {}, func(task.TokenUsage) {})
		close(watchDone)
	}()

	summary, err := cc.SendPrompt(context.Background(), server.URL, "proxy-id", "", task.TaskRequest{Prompt: "test"}, t.TempDir(), 5*time.Second)
	if err != nil {
		t.Fatalf("SendPrompt returned error: %v", err)
	}
	if summary != "terminal summary" {
		t.Fatalf("summary = %q, want terminal summary", summary)
	}
	select {
	case <-watchDone:
	case <-time.After(time.Second):
		t.Fatal("event watcher did not finish")
	}
}

func TestSendPromptCompletionEventReleasesHangingResponse(t *testing.T) {
	promptStarted := make(chan struct{})
	releaseHandler := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/event", func(w http.ResponseWriter, _ *http.Request) {
		<-promptStarted
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: result\ndata: {\"usage\":{\"input_tokens\":2}}\n\n")
		fmt.Fprint(w, "event: completed\ndata: {\"subtype\":\"success\",\"result\":\"terminal summary\"}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	})
	mux.HandleFunc("/session/proxy-id/message", func(_ http.ResponseWriter, _ *http.Request) {
		close(promptStarted)
		<-releaseHandler
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	cc := New()
	idleCh := make(chan struct{})
	cc.SetCompletionContext("proxy-id", idleCh, func() { close(idleCh) })
	go cc.WatchEvents(context.Background(), "task", server.URL, "", func(string, string) {}, func(task.TokenUsage) {})

	started := time.Now()
	summary, err := cc.SendPrompt(context.Background(), server.URL, "proxy-id", "", task.TaskRequest{Prompt: "test"}, t.TempDir(), 10*time.Second)
	close(releaseHandler)
	if err != nil || summary != "terminal summary" {
		t.Fatalf("SendPrompt = %q, %v", summary, err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("terminal event did not promptly release request: %v", elapsed)
	}
}

func TestSendPromptHTTPCompletionSurvivesMissingTerminalEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"summary":"http summary"}`)
	}))
	defer server.Close()

	cc := New()
	idleCh := make(chan struct{})
	cc.SetCompletionContext("proxy-id", idleCh, func() { close(idleCh) })
	started := time.Now()
	summary, err := cc.SendPrompt(context.Background(), server.URL, "proxy-id", "", task.TaskRequest{Prompt: "test"}, t.TempDir(), 10*time.Second)
	if err != nil || summary != "http summary" {
		t.Fatalf("SendPrompt = %q, %v", summary, err)
	}
	elapsed := time.Since(started)
	if elapsed < 1900*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("terminal event grace period = %v", elapsed)
	}
}
