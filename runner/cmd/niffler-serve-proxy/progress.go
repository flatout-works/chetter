package main

import (
	"strings"
	"time"
)

const progressFlushInterval = 15 * time.Second
const maxProgressBuffer = 64 << 10

// Native structured frames are timeline records. Token deltas only provide
// bounded liveness and a partial-output fallback when no assistant frame lands.
type progressBuffer struct {
	text      strings.Builder
	thinking  strings.Builder
	lastFlush time.Time
	terminal  map[string]bool
}

func (p *progressBuffer) token(now time.Time, content, reasoning string, emit func(string)) {
	if p.lastFlush.IsZero() {
		p.lastFlush = now
	}
	appendBounded := func(b *strings.Builder, s string) {
		space := maxProgressBuffer - b.Len()
		if space > 0 {
			if len(s) > space {
				s = s[:space]
			}
			b.WriteString(s)
		}
	}
	appendBounded(&p.text, content)
	appendBounded(&p.thinking, reasoning)
	if now.Sub(p.lastFlush) >= progressFlushInterval && (content != "" || reasoning != "") {
		message := "Niffler is generating a response"
		if reasoning != "" {
			message = "Niffler is thinking"
		}
		emit(message)
		p.lastFlush = now
	}
}
func (p *progressBuffer) assistant(now time.Time, content string, emit func(string)) {
	// The canonical frame replaces pending deltas; text is emitted once as a
	// whole assistant message, never as a series of token fragments.
	if text := strings.TrimSpace(content); text != "" {
		emit("Niffler: " + text)
	}
	p.text.Reset()
	p.thinking.Reset()
	p.lastFlush = now
}
func (p *progressBuffer) flush(now time.Time, force bool, emit func(string)) {
	if !force {
		return
	}
	if text := strings.TrimSpace(p.thinking.String()); text != "" {
		emit("Niffler partial thinking: " + text)
	}
	if text := strings.TrimSpace(p.text.String()); text != "" {
		emit("Niffler partial response: " + text)
	}
	p.text.Reset()
	p.thinking.Reset()
	p.lastFlush = now
}
func (p *progressBuffer) settle(id string) bool {
	if id == "" {
		return false
	}
	if p.terminal == nil {
		p.terminal = map[string]bool{}
	}
	if p.terminal[id] {
		return false
	}
	p.terminal[id] = true
	return true
}
