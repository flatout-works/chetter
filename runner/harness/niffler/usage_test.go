package niffler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestTurnUsageOptionalCounters(t *testing.T) {
	var u TurnUsage
	if e := json.Unmarshal([]byte(`{"promptTokens":10,"completionTokens":4}`), &u); e != nil {
		t.Fatal(e)
	}
	if u.CacheReadTokens != nil || u.CacheWriteTokens != nil || u.ReasoningTokens != nil {
		t.Fatal("absent counter fabricated")
	}
	if e := json.Unmarshal([]byte(`{"promptTokens":10,"completionTokens":4,"cacheReadTokens":0,"cacheWriteTokens":2,"reasoningTokens":3}`), &u); e != nil {
		t.Fatal(e)
	}
	if u.CacheReadTokens == nil || *u.CacheReadTokens != 0 {
		t.Fatal("honest zero lost")
	}
	got := u.TaskUsage()
	if got.InputTokens != 10 || got.OutputTokens != 4 || got.CacheWriteTokens != 2 || got.ReasoningTokens != 3 {
		t.Fatal(got)
	}
}
func TestTurnAccountingIsIdempotentAndAddsActivations(t *testing.T) {
	n := New()
	var got task.TokenUsage
	n.tokens = func(u task.TokenUsage) {
		got.InputTokens += u.InputTokens
		got.CacheWriteTokens += u.CacheWriteTokens
		got.ReasoningTokens += u.ReasoningTokens
	}
	n.account("turn-1", task.TokenUsage{InputTokens: 10, CacheWriteTokens: 2, ReasoningTokens: 3})
	n.account("turn-1", task.TokenUsage{InputTokens: 10, CacheWriteTokens: 2, ReasoningTokens: 3})
	n.account("turn-2", task.TokenUsage{InputTokens: 4, CacheWriteTokens: 1, ReasoningTokens: 2})
	n.account("", task.TokenUsage{InputTokens: 100})
	if got.InputTokens != 14 || got.CacheWriteTokens != 3 || got.ReasoningTokens != 5 {
		t.Fatal(got)
	}
}
func TestFailedTurnAndAbortKeepUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session/sid/message" {
			w.WriteHeader(502)
		}
		fmt.Fprint(w, `{"error":"cancelled","turnId":"turn-1","outcome":"cancelled","usage":{"promptTokens":10,"completionTokens":4,"cacheWriteTokens":2,"reasoningTokens":3}}`)
	}))
	defer srv.Close()
	n := New()
	var got task.TokenUsage
	n.tokens = func(u task.TokenUsage) {
		got.InputTokens += u.InputTokens
		got.CacheWriteTokens += u.CacheWriteTokens
		got.ReasoningTokens += u.ReasoningTokens
	}
	r := testRequest()
	r.Prompt = "work"
	if _, e := n.SendPrompt(context.Background(), srv.URL, "sid", "secret", r, "", time.Second); e == nil {
		t.Fatal("cancelled turn accepted")
	}
	if e := n.AbortSession(context.Background(), srv.URL, "sid", "secret"); e != nil {
		t.Fatal(e)
	}
	if got.InputTokens != 10 || got.CacheWriteTokens != 2 || got.ReasoningTokens != 3 {
		t.Fatal(got)
	}
}
func TestReplyAndTerminalEventDoNotDoubleCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: usage\ndata: {\"turnId\":\"turn-1\",\"usage\":{\"promptTokens\":10,\"completionTokens\":4}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := New()
	n.account("turn-1", task.TokenUsage{InputTokens: 10, OutputTokens: 4})
	got := make(chan task.TokenUsage, 5)
	done := make(chan struct{})
	go func() {
		defer close(done)
		n.WatchEvents(ctx, "task", srv.URL, "secret", func(string, string) {}, func(u task.TokenUsage) { got <- u })
	}()
	select {
	case u := <-got:
		if u.InputTokens != 10 {
			t.Fatal(u)
		}
	case <-time.After(time.Second):
		t.Fatal("late callback lost final accounting")
	}
	select {
	case u := <-got:
		t.Fatalf("replayed usage counted: %+v", u)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	<-done
}
