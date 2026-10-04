package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestProgressBatchesDeltasAndDoesNotRepeatAssistant(t *testing.T) {
	now := time.Unix(100, 0)
	p := &progressBuffer{}
	var got []string
	emit := func(s string) { got = append(got, s) }
	for _, s := range []string{"Con", "firmed", " — ", "tool", " was ", "called"} {
		p.token(now, s, "", emit)
	}
	if len(got) != 0 {
		t.Fatalf("token deltas leaked: %v", got)
	}
	p.assistant(now, "Confirmed — tool was called", emit)
	if !reflect.DeepEqual(got, []string{"niffler: Confirmed — tool was called"}) {
		t.Fatal(got)
	}
	p.flush(now, true, emit)
	if len(got) != 1 {
		t.Fatal("repeated flush duplicated text", got)
	}
}
func TestProgressHealsMissingLastDeltaWithoutRepeating(t *testing.T) {
	start := time.Unix(100, 0)
	p := &progressBuffer{}
	var got []string
	emit := func(s string) { got = append(got, s) }
	p.token(start, "Hel", "", emit)
	p.token(start.Add(progressFlushInterval), "lo wor", "", emit) // triggers flush of "Hel"+"lo wor"
	p.token(start.Add(progressFlushInterval), "ld", "", emit)     // buffer now has "ld"
	p.assistant(start.Add(progressFlushInterval+time.Second), "Hello world", emit)
	// First flush emitted "Hello wor"; assistant should add only the "ld" suffix
	if !reflect.DeepEqual(got, []string{"niffler: Hello wor", "niffler: ld"}) {
		t.Fatal(got)
	}
}
func TestProgressResetsAfterAssistantFrame(t *testing.T) {
	now := time.Unix(100, 0)
	p := &progressBuffer{}
	var got []string
	emit := func(s string) { got = append(got, s) }
	p.assistant(now, "first", emit)
	p.token(now, "second", "", emit)
	p.assistant(now, "second", emit)
	if !reflect.DeepEqual(got, []string{"niffler: first", "niffler: second"}) {
		t.Fatal(got)
	}
}
func TestProgressIntervalKeepsWatchdogActive(t *testing.T) {
	start := time.Unix(100, 0)
	p := &progressBuffer{}
	var got []string
	emit := func(s string) { got = append(got, s) }
	p.token(start, "Hello", "", emit)
	p.token(start.Add(2*time.Second), " world", "", emit)
	if len(got) != 0 {
		t.Fatal(got)
	}
	p.token(start.Add(3*time.Second), "!", "", emit)
	p.assistant(start.Add(4*time.Second), "Hello world!", emit)
	if !reflect.DeepEqual(got, []string{"niffler: Hello world!"}) {
		t.Fatal(got)
	}
	p.assistant(start.Add(5*time.Second), "Next answer", emit)
	if got[len(got)-1] != "niffler: Next answer" {
		t.Fatal(got)
	}
}
func TestProgressThinkingAndBoundedBuffers(t *testing.T) {
	now := time.Unix(100, 0)
	p := &progressBuffer{}
	var got []string
	emit := func(s string) { got = append(got, s) }
	p.token(now, "", "Think ", emit)
	p.token(now, "", "carefully.", emit)
	p.token(now, "Answer", "", emit)
	p.flush(now, true, emit)
	if !reflect.DeepEqual(got, []string{"niffler thinking: Think carefully.", "niffler: Answer"}) {
		t.Fatal(got)
	}
	p.token(now, strings.Repeat("x", maxProgressBuffer), "", emit)
	if p.text.Len() != 0 {
		t.Fatal("buffer not bounded")
	}
}
func TestObserveProgressBoundariesAndRedaction(t *testing.T) {
	for _, boundary := range []string{"assistant", "toolcall", "turn", "driver-exit"} {
		t.Run(boundary, func(t *testing.T) {
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
			if len(b.history) != 0 {
				t.Fatal("unbuffered deltas", b.history)
			}
			switch boundary {
			case "assistant":
				send("assistant", map[string]any{"content": "secret says hello"})
			case "toolcall":
				send("toolcall", map[string]any{"phase": "start", "tool": "read"})
			case "turn":
				send("turn", map[string]any{"phase": "done", "turnId": "t1", "usage": map[string]any{"promptTokens": 10}})
			case "driver-exit":
				p.flush(now, true, b.progressMessage)
			}
			var first struct {
				Message string `json:"message"`
			}
			if len(b.history) == 0 {
				t.Fatal("partial text lost")
			}
			if e := json.Unmarshal(b.history[0].Data, &first); e != nil {
				t.Fatal(e)
			}
			if first.Message != "niffler: [REDACTED] says hello" {
				t.Fatal(first.Message)
			}
			if boundary == "toolcall" && !strings.Contains(string(b.history[1].Data), "tool start: read") {
				t.Fatal("tool event lost")
			}
			if boundary == "turn" && b.history[1].Type != "usage" {
				t.Fatal("accounting changed")
			}
		})
	}
}
func TestObserveProgressIgnoresOtherConversations(t *testing.T) {
	b := &bridge{listeners: map[chan event]bool{}}
	p := &progressBuffer{}
	raw := json.RawMessage(`{"sessionId":"child","content":"unrelated"}`)
	b.observeProgress("ev.session.child.token", raw, "parent", p, time.Unix(100, 0))
	p.flush(time.Unix(100, 0), true, b.progressMessage)
	if len(b.history) != 0 {
		t.Fatal(b.history)
	}
}
