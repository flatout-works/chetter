package controller

import (
	"context"
	"errors"
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

	// maxHarnessContinuationStreak bounds consecutive continuation prompts
	// sent during a single unbroken stall. Real harness progress resets the
	// streak, so a task that stalls, recovers, and stalls again later gets a
	// fresh budget instead of inheriting a near-exhausted one.
	maxHarnessContinuationStreak = 3
	// maxHarnessContinuationAttemptsTotal bounds continuation prompts across
	// a whole run so a harness that only ever moves when prodded cannot loop
	// indefinitely.
	maxHarnessContinuationAttemptsTotal = 10
	// harnessProgressBusyFailAfter is how long the watchdog tolerates a
	// silent harness whose server reports an in-flight generation turn
	// (probe says busy). Slow model providers can reason quietly for several
	// minutes; nudging such a session does nothing, but far beyond this
	// window the in-flight request has almost certainly hung.
	harnessProgressBusyFailAfter = 12 * time.Minute
)

// harnessStuckCause records why the watchdog terminated a stalled task, so
// reports and failure payloads can carry a precise, honest reason.
type harnessStuckCause int

const (
	stuckCauseNone                          harnessStuckCause = iota
	stuckCauseSilentAfterNudge                                 // no progress within failAfter of the latest nudge
	stuckCauseContinuationAttemptsExhausted                    // silent past the nudge window with the budget spent
	stuckCauseBusyUnresponsive                                 // harness reports an in-flight turn but nothing arrives
)

// progressWatchdog distinguishes harness activity from runner heartbeats. A
// heartbeat only proves the harness server is alive; it does not show that the
// agent is still advancing the task.
type progressWatchdog struct {
	mu             sync.Mutex
	now            func() time.Time
	lastProgress   time.Time
	nudgedAt       time.Time
	nudgeCount     int // nudges since the last real progress event
	nudgeTotal     int // nudges across the whole run; never reset
	stuck          bool
	stuckCause     harnessStuckCause
	stuckSilence   time.Duration
	turnStateKnown bool
	turnActive     bool
	lastSummary    string
	nudge          func(context.Context) error
	report         func(string)
	cancel         context.CancelFunc
	stopContext    context.CancelFunc
	isIdle         func() bool
	probe          func(context.Context) (string, error)
	done           chan struct{}
	stopped        chan struct{}
	stopOnce       sync.Once
}

func startProgressWatchdog(ctx context.Context, cancel context.CancelFunc, nudge func(context.Context) error, report func(string), isIdle func() bool, probe func(context.Context) (string, error)) *progressWatchdog {
	watchCtx, stopContext := context.WithCancel(ctx)
	watchdog := &progressWatchdog{
		now:          time.Now,
		lastProgress: time.Now(),
		nudge:        nudge,
		report:       report,
		cancel:       cancel,
		stopContext:  stopContext,
		isIdle:       isIdle,
		probe:        probe,
		done:         make(chan struct{}),
		stopped:      make(chan struct{}),
	}
	go func() {
		defer close(watchdog.stopped)
		watchdog.run(watchCtx)
	}()
	return watchdog
}

func (w *progressWatchdog) stop() {
	w.stopOnce.Do(func() {
		close(w.done)
		w.stopContext()
	})
	<-w.stopped
}

func (w *progressWatchdog) record(summary string) {
	if !isHarnessProgress(summary) {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastProgress = w.now()
	w.lastSummary = truncateWatchdogSummary(summary)
	if strings.Contains(summary, "session.status") {
		w.turnStateKnown = true
		switch {
		case strings.Contains(summary, `"busy"`):
			w.turnActive = true
		case strings.Contains(summary, `"idle"`):
			w.turnActive = false
		}
	}
	// A post-nudge event means the agent recovered. A later stall gets a new
	// continuation attempt rather than inheriting the previous deadline, so
	// reset the per-stall nudge budget too. nudgeTotal keeps accruing to
	// bound harnesses that only ever move when prodded.
	w.nudgedAt = time.Time{}
	w.nudgeCount = 0
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
			nudge, failCause := w.check(w.now())
			if nudge {
				w.report(fmt.Sprintf("No harness progress for %s; sending continuation prompt.", harnessProgressNudgeAfter))
				nudgeCtx, cancel := context.WithTimeout(ctx, harnessContinueTimeout)
				err := w.nudge(nudgeCtx)
				cancel()
				if err != nil && ctx.Err() == nil {
					w.report(fmt.Sprintf("Continuation prompt failed: %v", err))
				}
			}
			if failCause != stuckCauseNone {
				w.fail(failCause)
				w.report(w.stuckMessage())
				w.cancel()
				return
			}
		}
	}
}

func (w *progressWatchdog) check(now time.Time) (nudge bool, failCause harnessStuckCause) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.isIdle != nil && w.isIdle() {
		return false, stuckCauseNone
	}
	if !w.nudgedAt.IsZero() {
		if now.Sub(w.nudgedAt) >= harnessProgressFailAfter {
			return false, stuckCauseSilentAfterNudge
		}
		return false, stuckCauseNone
	}
	elapsed := now.Sub(w.lastProgress)
	if elapsed < harnessProgressNudgeAfter {
		return false, stuckCauseNone
	}
	if w.nudge == nil {
		// A harness that cannot continue owns its completion timeout. Do not
		// terminate legitimate long-running work based only on quiet output.
		return false, stuckCauseNone
	}
	if w.nudgeCount >= maxHarnessContinuationStreak || w.nudgeTotal >= maxHarnessContinuationAttemptsTotal {
		return false, stuckCauseContinuationAttemptsExhausted
	}
	if active, known := w.probeTurnActive(); known && active {
		// The harness server reports an in-flight generation turn. Quiet
		// reasoning or a slow provider is expected here; only a far longer
		// silence means the in-flight request has hung.
		if elapsed >= harnessProgressBusyFailAfter {
			return false, stuckCauseBusyUnresponsive
		}
		return false, stuckCauseNone
	}
	w.nudgedAt = now
	w.nudgeCount++
	w.nudgeTotal++
	return true, stuckCauseNone
}

// probeTurnActive asks the harness whether a generation turn is in flight.
// A nil, failed, or stale probe degrades to "unknown" and falls back to the
// nudge/fail logic alone.
func (w *progressWatchdog) probeTurnActive() (active, known bool) {
	if w.probe == nil {
		return false, false
	}
	probeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	status, err := w.probe(probeCtx)
	cancel()
	if err != nil {
		return false, false
	}
	if status == "busy" {
		return true, true
	}
	return false, true
}

func (w *progressWatchdog) fail(cause harnessStuckCause) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.stuck = true
	w.stuckCause = cause
	if cause == stuckCauseSilentAfterNudge {
		w.stuckSilence = harnessProgressFailAfter
	} else {
		w.stuckSilence = w.now().Sub(w.lastProgress)
	}
}

func (w *progressWatchdog) isStuck() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stuck
}

// stuckMessage renders the human-facing progress event emitted when the
// watchdog terminates a task. It preserves the legacy
// "Harness made no progress" phrasing where it is accurate so existing
// log-based alerting keeps matching.
func (w *progressWatchdog) stuckMessage() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch w.stuckCause {
	case stuckCauseBusyUnresponsive:
		return fmt.Sprintf("Harness busy but unresponsive: no progress for %s during an in-flight generation turn (likely a hung model request); stopping task.", harnessProgressBusyFailAfter)
	case stuckCauseContinuationAttemptsExhausted:
		if w.turnStateKnown && w.turnActive {
			// Never nudge a busy harness: a real turn is in flight and its
			// own completion timeout applies.
			return fmt.Sprintf("Harness made no progress for %s after continuation prompt; stopping task.", harnessProgressFailAfter)
		}
		return fmt.Sprintf("Harness continuation prompt budget exhausted (%d consecutive, %d/%d total) after %s without progress; stopping task.",
			maxHarnessContinuationStreak, w.nudgeTotal, maxHarnessContinuationAttemptsTotal, harnessProgressFailAfter)
	default:
		return fmt.Sprintf("Harness made no progress for %s after continuation prompt; stopping task.", harnessProgressFailAfter)
	}
}

// stuckDetails renders the quantified diagnostic string embedded in terminal
// task failure payloads, so a post-mortem can distinguish a hung in-flight
// generation from an agent that idled and ignored continuation prompts.
func (w *progressWatchdog) stuckDetails() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.stuck {
		return ""
	}
	var cause string
	switch w.stuckCause {
	case stuckCauseBusyUnresponsive:
		cause = "busy harness unresponsive"
	case stuckCauseContinuationAttemptsExhausted:
		cause = "continuation prompt budget exhausted"
	default:
		cause = "no progress after continuation prompt"
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s; silent for %s", cause, w.stuckSilence.Round(time.Second)))
	if w.nudgeTotal > 0 {
		b.WriteString(fmt.Sprintf("; %d/%d continuation prompts sent", w.nudgeTotal, maxHarnessContinuationAttemptsTotal))
	}
	if w.lastSummary != "" {
		b.WriteString(fmt.Sprintf("; last event: %q", w.lastSummary))
	}
	if w.turnStateKnown {
		if w.turnActive {
			b.WriteString("; harness turn in flight — likely a hung or very slow model request")
		} else {
			b.WriteString("; harness idle — agent completed its turn but kept ignoring the runner")
		}
	}
	return b.String()
}

// stuckError builds the terminal "stuck harness" error while preserving the
// legacy prefix that server-side failure classification matches on.
func (w *progressWatchdog) stuckError() error {
	if detail := w.stuckDetails(); detail != "" {
		return fmt.Errorf("stuck harness: %s", detail)
	}
	return errors.New("stuck harness: no progress")
}

func isHarnessProgress(summary string) bool {
	summary = strings.TrimSpace(strings.ToLower(summary))
	return summary != "" && !strings.Contains(summary, "heartbeat")
}

const maxWatchdogSummaryLen = 160

func truncateWatchdogSummary(summary string) string {
	if len(summary) > maxWatchdogSummaryLen {
		return summary[:maxWatchdogSummaryLen] + "..."
	}
	return summary
}
