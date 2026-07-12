package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	harnessProgressCheckInterval = 10 * time.Second
	harnessProgressNudgeAfter    = 2 * time.Minute
	harnessProgressFailAfter     = 5 * time.Minute
	harnessContinueTimeout       = 30 * time.Second
)

// progressWatchdog distinguishes harness activity from runner heartbeats. A
// heartbeat only proves the harness server is alive; it does not show that the
// agent is still advancing the task.
type progressWatchdog struct {
	mu           sync.Mutex
	now          func() time.Time
	lastProgress time.Time
	nudgedAt     time.Time
	stuck        bool
	nudge        func(context.Context) error
	report       func(string)
	cancel       context.CancelFunc
	done         chan struct{}
	stopOnce     sync.Once
}

func startProgressWatchdog(ctx context.Context, cancel context.CancelFunc, nudge func(context.Context) error, report func(string)) *progressWatchdog {
	watchdog := &progressWatchdog{
		now:          time.Now,
		lastProgress: time.Now(),
		nudge:        nudge,
		report:       report,
		cancel:       cancel,
		done:         make(chan struct{}),
	}
	go watchdog.run(ctx)
	return watchdog
}

func (w *progressWatchdog) stop() {
	w.stopOnce.Do(func() { close(w.done) })
}

func (w *progressWatchdog) record(summary string) {
	if !isHarnessProgress(summary) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastProgress = w.now()
	// A post-nudge event means the agent recovered. A later stall gets a new
	// continuation attempt rather than inheriting the previous deadline.
	if !w.nudgedAt.IsZero() {
		w.nudgedAt = time.Time{}
	}
}

func (w *progressWatchdog) run(ctx context.Context) {
	ticker := time.NewTicker(harnessProgressCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-w.done:
			return
		case <-ticker.C:
			nudge, fail := w.check(w.now())
			if nudge {
				w.report(fmt.Sprintf("No harness progress for %s; sending continuation prompt.", harnessProgressNudgeAfter))
				nudgeCtx, cancel := context.WithTimeout(ctx, harnessContinueTimeout)
				err := w.nudge(nudgeCtx)
				cancel()
				if err != nil {
					w.report(fmt.Sprintf("Continuation prompt failed: %v", err))
				}
			}
			if fail {
				w.mu.Lock()
				w.stuck = true
				w.mu.Unlock()
				w.report(fmt.Sprintf("Harness made no progress for %s after continuation prompt; stopping task.", harnessProgressFailAfter))
				w.cancel()
				return
			}
		}
	}
}

func (w *progressWatchdog) check(now time.Time) (nudge, fail bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.nudgedAt.IsZero() {
		if now.Sub(w.lastProgress) >= harnessProgressNudgeAfter {
			w.nudgedAt = now
			return true, false
		}
		return false, false
	}
	return false, now.Sub(w.nudgedAt) >= harnessProgressFailAfter
}

func (w *progressWatchdog) isStuck() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stuck
}

func isHarnessProgress(summary string) bool {
	summary = strings.TrimSpace(strings.ToLower(summary))
	return summary != "" && !strings.Contains(summary, "heartbeat")
}
