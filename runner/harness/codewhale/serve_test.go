package codewhale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestWaitForTurnCompletionReturnsSummaryAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/threads/thread_1/events" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("since_seq") != "0" {
			t.Fatalf("unexpected since_seq: %s", r.URL.RawQuery)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			sseFrame("item.delta", map[string]any{
				"seq":     1,
				"turn_id": "turn_1",
				"payload": map[string]any{
					"kind":  "agent_message",
					"delta": "hello ",
				},
			})+
				sseFrame("item.delta", map[string]any{
					"seq":     2,
					"turn_id": "turn_1",
					"payload": map[string]any{
						"kind":  "agent_message",
						"delta": "world",
					},
				})+
				sseFrame("turn.completed", map[string]any{
					"seq":     3,
					"turn_id": "turn_1",
					"payload": map[string]any{
						"turn": map[string]any{
							"status": "completed",
							"usage": map[string]any{
								"input_tokens":            10,
								"output_tokens":           5,
								"prompt_cache_hit_tokens": 3,
								"reasoning_tokens":        2,
							},
							"cost_usd": 0.12,
						},
					},
				}),
		)
	}))
	defer server.Close()

	var usage task.TokenUsage
	summary, err := waitForTurnCompletion(context.Background(), server.URL, "thread_1", "turn_1", "secret", nil, func(u task.TokenUsage) {
		usage = u
	})
	if err != nil {
		t.Fatalf("waitForTurnCompletion returned error: %v", err)
	}
	if summary != "hello world" {
		t.Fatalf("unexpected summary: %q", summary)
	}
	if usage.InputTokens != 10 || usage.OutputTokens != 5 || usage.CacheReadTokens != 3 || usage.ReasoningTokens != 2 || usage.CostCents != 12 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestWaitForTurnCompletionReturnsTerminalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, sseFrame("turn.completed", map[string]any{
			"seq":     1,
			"turn_id": "turn_1",
			"payload": map[string]any{
				"turn": map[string]any{
					"status": "failed",
					"error":  "boom",
				},
			},
		}))
	}))
	defer server.Close()

	_, err := waitForTurnCompletion(context.Background(), server.URL, "thread_1", "turn_1", "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "turn failed: boom") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForTurnCompletionReconnectsFromLastSequence(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := requests.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		switch request {
		case 1:
			if got := r.URL.Query().Get("since_seq"); got != "0" {
				t.Errorf("first since_seq = %q, want 0", got)
			}
			_, _ = fmt.Fprint(w, sseFrame("item.delta", map[string]any{
				"seq":     1,
				"turn_id": "turn_1",
				"payload": map[string]any{"kind": "agent_message", "delta": "hello"},
			}))
		case 2:
			if got := r.URL.Query().Get("since_seq"); got != "1" {
				t.Errorf("second since_seq = %q, want 1", got)
			}
			_, _ = fmt.Fprint(w,
				sseFrame("item.delta", map[string]any{
					"seq":     1,
					"turn_id": "turn_1",
					"payload": map[string]any{"kind": "agent_message", "delta": "duplicate"},
				})+
					sseFrame("item.delta", map[string]any{
						"seq":     2,
						"turn_id": "another_turn",
						"payload": map[string]any{"kind": "agent_message", "delta": "wrong turn"},
					})+
					sseFrame("turn.completed", map[string]any{
						"seq":     3,
						"turn_id": "turn_1",
						"payload": map[string]any{"turn": map[string]any{
							"status": "completed",
							"usage":  map[string]any{"input_tokens": 7, "output_tokens": 4},
						}},
					}),
			)
		default:
			t.Errorf("unexpected event request %d", request)
		}
	}))
	defer server.Close()

	var progress []string
	var usage task.TokenUsage
	summary, err := waitForTurnCompletion(context.Background(), server.URL, "thread_1", "turn_1", "", func(_ string, message string) {
		progress = append(progress, message)
	}, func(got task.TokenUsage) {
		usage = got
	})
	if err != nil {
		t.Fatalf("waitForTurnCompletion returned error: %v", err)
	}
	if summary != "hello" {
		t.Fatalf("summary = %q, want hello", summary)
	}
	if requests.Load() != 2 {
		t.Fatalf("event requests = %d, want 2", requests.Load())
	}
	if len(progress) != 1 || progress[0] != "codewhale: hello" {
		t.Fatalf("unexpected progress: %q", progress)
	}
	if usage.InputTokens != 7 || usage.OutputTokens != 4 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestWaitForTurnCompletionRejectsUnknownStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, sseFrame("turn.completed", map[string]any{
			"seq":     1,
			"turn_id": "turn_1",
			"payload": map[string]any{"turn": map[string]any{"status": "finishing"}},
		}))
	}))
	defer server.Close()

	_, err := waitForTurnCompletion(context.Background(), server.URL, "thread_1", "turn_1", "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), `unknown status "finishing"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitForTurnCompletionCancellation(t *testing.T) {
	connected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		close(connected)
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := waitForTurnCompletion(ctx, server.URL, "thread_1", "turn_1", "", nil, nil)
		result <- err
	}()
	<-connected
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("waitForTurnCompletion did not stop after cancellation")
	}
}

func TestSendPromptWaitsForWatchEventsCallbacks(t *testing.T) {
	turnPosted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/threads/thread_1/turns":
			turnPosted <- struct{}{}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{"turn":{"id":"turn_1"}}`)
		case "/v1/threads/thread_1/events":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w,
				sseFrame("item.delta", map[string]any{
					"seq":     1,
					"turn_id": "turn_1",
					"payload": map[string]any{"kind": "agent_message", "delta": "done"},
				})+
					sseFrame("turn.completed", map[string]any{
						"seq":     2,
						"turn_id": "turn_1",
						"payload": map[string]any{"turn": map[string]any{
							"status": "completed",
							"usage":  map[string]any{"input_tokens": 2},
						}},
					}),
			)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cw := New()
	result := make(chan struct {
		summary string
		err     error
	}, 1)
	go func() {
		summary, err := cw.SendPrompt(context.Background(), server.URL, "thread_1", "", task.TaskRequest{Prompt: "work"}, "", 2*time.Second)
		result <- struct {
			summary string
			err     error
		}{summary: summary, err: err}
	}()

	select {
	case <-turnPosted:
		t.Fatal("turn was posted before WatchEvents registered callbacks")
	case <-time.After(50 * time.Millisecond):
	}

	watchCtx, stopWatch := context.WithCancel(context.Background())
	defer stopWatch()
	progress := make(chan string, 1)
	usage := make(chan task.TokenUsage, 1)
	go cw.WatchEvents(watchCtx, "task_1", server.URL, "", func(_ string, message string) {
		progress <- message
	}, func(got task.TokenUsage) {
		usage <- got
	})

	select {
	case got := <-result:
		if got.err != nil {
			t.Fatalf("SendPrompt returned error: %v", got.err)
		}
		if got.summary != "done" {
			t.Fatalf("summary = %q, want done", got.summary)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("SendPrompt did not complete")
	}
	if got := <-progress; got != "codewhale: done" {
		t.Fatalf("progress = %q, want codewhale: done", got)
	}
	if got := <-usage; got.InputTokens != 2 {
		t.Fatalf("unexpected usage: %+v", got)
	}
}

func TestReadSessionExportUsesObservedTurnFallback(t *testing.T) {
	cw := New()
	cw.setSessionExport("thread_1", renderMarkdownExport("thread_1", "turn_1", "do work", "done", nil))

	export, err := cw.ReadSessionExport(t.TempDir(), "thread_1")
	if err != nil {
		t.Fatalf("ReadSessionExport returned error: %v", err)
	}
	for _, want := range []string{"# CodeWhale Session", "Thread: `thread_1`", "Turn: `turn_1`", "## User", "do work", "## Assistant", "done"} {
		if !strings.Contains(export, want) {
			t.Fatalf("export missing %q:\n%s", want, export)
		}
	}
}

func sseFrame(event string, data any) string {
	encoded, _ := json.Marshal(data)
	return fmt.Sprintf("event: %s\ndata: %s\n\n", event, encoded)
}
