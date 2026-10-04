package main

import (
	"strings"
	"time"
)

const progressFlushInterval = 3 * time.Second
const maxProgressBuffer = 64 << 10

// One native turn owns this accumulator. Tokens are deltas, not timeline
// messages; only coherent batches and completed assistant messages are emitted.
// Tool/terminal boundaries and driver exit flush partial output as well.
type progressBuffer struct {
	text      strings.Builder
	thinking  strings.Builder
	lastFlush time.Time
	// tokenBytes counts bytes of this assistant round consumed by token()
	// deltas. assistant() emits only the suffix beyond this count, healing a
	// missing trailing delta without repeating already-flushed text.
	tokenBytes int
}

func (p *progressBuffer) flush(now time.Time, force bool, emit func(string)) {
	if !force && now.Sub(p.lastFlush) < progressFlushInterval && p.text.Len()+p.thinking.Len() < maxProgressBuffer {
		return
	}
	if s := strings.TrimSpace(p.thinking.String()); s != "" {
		emit("niffler thinking: " + s)
	}
	if s := strings.TrimSpace(p.text.String()); s != "" {
		emit("niffler: " + s)
	}
	p.text.Reset()
	p.thinking.Reset()
	p.lastFlush = now
}

func (p *progressBuffer) token(now time.Time, content, reasoning string, emit func(string)) {
	if p.lastFlush.IsZero() {
		p.lastFlush = now
	}
	if content != "" {
		p.tokenBytes += len(content)
		p.text.WriteString(content)
	}
	p.thinking.WriteString(reasoning)
	p.flush(now, false, emit)
}

func (p *progressBuffer) assistant(now time.Time, content string, emit func(string)) {
	// The full frame may heal a missing last delta. Already emitted bytes
	// are counted across timed flushes, so they are never repeated.
	if len(content) > p.tokenBytes {
		p.text.WriteString(content[p.tokenBytes:])
	}
	p.flush(now, true, emit)
	p.tokenBytes = 0
}
