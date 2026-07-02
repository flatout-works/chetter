package codewhale

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
