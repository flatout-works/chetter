package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTokensAreLivenessNotTimelineFragments(t *testing.T) {
	p := &progressBuffer{}
	now := time.Unix(100, 0)
	var got []string
	emit := func(s string) { got = append(got, s) }
	for _, s := range []string{"Let", " me", " think", " /b"} {
		p.token(now, "", s, emit)
	}
	if len(got) != 0 {
		t.Fatal(got)
	}
	p.token(now.Add(progressFlushInterval), "Confirmed", "", emit)
	if !reflect.DeepEqual(got, []string{"Niffler is generating a response"}) {
		t.Fatal(got)
	}
	p.assistant(now.Add(16*time.Second), "Confirmed — tool called once", emit)
	if got[1] != "Niffler: Confirmed — tool called once" {
		t.Fatal(got)
	}
	p.flush(now, true, emit)
	if len(got) != 2 {
		t.Fatal("deltas repeated", got)
	}
}
func TestCanonicalAssistantFillsMissingDeltas(t *testing.T) {
	p := &progressBuffer{}
	now := time.Unix(100, 0)
	var got []string
	emit := func(s string) { got = append(got, s) }
	p.token(now, "Con", "", emit)
	p.assistant(now, "Confirmed", emit)
	p.assistant(now, "Second round", emit)
	if !reflect.DeepEqual(got, []string{"Niffler: Confirmed", "Niffler: Second round"}) {
		t.Fatal(got)
	}
}
func TestPartialFallbackAndBounds(t *testing.T) {
	p := &progressBuffer{}
	now := time.Unix(100, 0)
	var got []string
	emit := func(s string) { got = append(got, s) }
	p.token(now, "partial", "reason", emit)
	p.flush(now, true, emit)
	if !reflect.DeepEqual(got, []string{"Niffler partial thinking: reason", "Niffler partial response: partial"}) {
		t.Fatal(got)
	}
	p.flush(now, true, emit)
	if len(got) != 2 {
		t.Fatal(got)
	}
	p.token(now, strings.Repeat("x", maxProgressBuffer*2), "", emit)
	if p.text.Len() > maxProgressBuffer {
		t.Fatal("unbounded")
	}
}
func TestStructuredTimelineAndTerminalDedupe(t *testing.T) {
	for _, outcome := range []string{"success", "cancelled", "budget-exhausted", "error"} {
		t.Run(outcome, func(t *testing.T) {
			b := &bridge{secrets: []string{"secret"}, listeners: map[chan event]bool{}}
			p := &progressBuffer{}
			now := time.Unix(100, 0)
			send := func(kind string, data map[string]any) {
				data["sessionId"] = "sid"
				raw, _ := json.Marshal(data)
				b.observeProgress("ev.session.sid."+kind, raw, "sid", p, now)
			}
			send("token", map[string]any{"content": "se"})
			send("token", map[string]any{"content": "cret says hello"})
			send("assistant", map[string]any{"content": "secret says hello"})
			send("toolcall", map[string]any{"turnId": "turn1", "callId": "c1", "phase": "start", "tool": "bash"})
			send("toolcall", map[string]any{"turnId": "turn1", "callId": "c1", "phase": "done", "tool": "bash", "durationMs": 123})
			usage := json.RawMessage(`{"promptTokens":10,"reasoningTokens":2}`)
			send("turn", map[string]any{"turnId": "turn1", "phase": "done", "outcome": outcome, "usage": usage})
			b.settleProgress(p, "turn1", outcome, "", usage, now)
			if len(b.history) != 5 {
				t.Fatalf("expected assistant/tool start/tool done/terminal/usage, got %v", b.history)
			}
			var first struct {
				Message string `json:"message"`
			}
			json.Unmarshal(b.history[0].Data, &first)
			if first.Message != "Niffler: [REDACTED] says hello" {
				t.Fatal(first)
			}
			if !strings.Contains(string(b.history[1].Data), "Using bash") || !strings.Contains(string(b.history[2].Data), "Finished tool call bash") || !strings.Contains(string(b.history[2].Data), `"callId":"c1"`) {
				t.Fatal(b.history)
			}
			if !strings.Contains(string(b.history[3].Data), outcome) || b.history[4].Type != "usage" {
				t.Fatal(b.history)
			}
		})
	}
}
func TestOtherConversationsDoNotEnterTimeline(t *testing.T) {
	b := &bridge{listeners: map[chan event]bool{}}
	p := &progressBuffer{}
	raw := json.RawMessage(`{"sessionId":"child","content":"unrelated"}`)
	b.observeProgress("ev.session.child.assistant", raw, "parent", p, time.Unix(100, 0))
	if len(b.history) != 0 {
		t.Fatal(b.history)
	}
}
