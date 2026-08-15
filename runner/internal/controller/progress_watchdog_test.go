package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestIsHarnessProgress(t *testing.T) {
	for _, tc := range []struct {
		summary string
		want    bool
	}{
		{"opencode: server.heartbeat", false},
		{"claude: heartbeat", false},
		{"opencode: session.status {\"type\":\"busy\"}", true},
		{"opencode: message.part.updated", true},
		{"", false},
	} {
		if got := isHarnessProgress(tc.summary); got != tc.want {
			t.Errorf("isHarnessProgress(%q) = %v, want %v", tc.summary, got, tc.want)
		}
	}
}

func TestProgressWatchdogNudgesThenFails(t *testing.T) {
	start := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	now := start
	w := &progressWatchdog{
		now:          func() time.Time { return now },
		lastProgress: start,
		nudge:        func(context.Context) error { return nil },
	}

	nudge, cause := w.check(start.Add(harnessProgressNudgeAfter))
	if !nudge || cause != stuckCauseNone {
		t.Fatalf("first silent interval = nudge:%v cause:%v, want nudge only", nudge, cause)
	}

	nudge, cause = w.check(start.Add(harnessProgressNudgeAfter + harnessProgressFailAfter))
	if nudge || cause != stuckCauseSilentAfterNudge {
		t.Fatalf("post-nudge silent interval = nudge:%v cause:%v, want silent-after-nudge fail only", nudge, cause)
	}

	now = start.Add(harnessProgressNudgeAfter + time.Second)
	w.record("opencode: message.part.updated")
	nudge, cause = w.check(now.Add(harnessProgressNudgeAfter))
	if !nudge || cause != stuckCauseNone {
		t.Fatalf("progress after nudge should reset watchdog, got nudge:%v cause:%v", nudge, cause)
	}
}

func TestProgressWatchdogWaitsWithoutNudge(t *testing.T) {
	start := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	w := &progressWatchdog{lastProgress: start}

	nudge, cause := w.check(start.Add(harnessProgressNudgeAfter))
	if nudge || cause != stuckCauseNone {
		t.Fatalf("first silent interval = nudge:%v cause:%v, want neither", nudge, cause)
	}

	nudge, cause = w.check(start.Add(harnessProgressFailAfter))
	if nudge || cause != stuckCauseNone {
		t.Fatalf("no-nudge timeout = nudge:%v cause:%v, want neither", nudge, cause)
	}
}

func TestProgressWatchdogNoNudgeWhenIdle(t *testing.T) {
	start := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	w := &progressWatchdog{
		now:          func() time.Time { return start },
		lastProgress: start,
		nudge:        func(context.Context) error { t.Fatal("should not nudge when idle"); return nil },
		isIdle:       func() bool { return true },
	}

	nudge, cause := w.check(start.Add(harnessProgressNudgeAfter))
	if nudge || cause != stuckCauseNone {
		t.Fatalf("idle watchdog = nudge:%v cause:%v, want neither", nudge, cause)
	}

	nudge, cause = w.check(start.Add(harnessProgressNudgeAfter + harnessProgressFailAfter))
	if nudge || cause != stuckCauseNone {
		t.Fatalf("idle watchdog at fail threshold = nudge:%v cause:%v, want neither", nudge, cause)
	}
}

func TestProgressWatchdogNudgesWhenNotIdle(t *testing.T) {
	start := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	w := &progressWatchdog{
		now:          func() time.Time { return start },
		lastProgress: start,
		nudge:        func(context.Context) error { return nil },
		isIdle:       func() bool { return false },
	}

	nudge, cause := w.check(start.Add(harnessProgressNudgeAfter))
	if !nudge || cause != stuckCauseNone {
		t.Fatalf("not-idle watchdog = nudge:%v cause:%v, want nudge only", nudge, cause)
	}
}

// TestProgressWatchdogResetsNudgeBudgetOnProgress is the regression test for
// the incident where a task exhausted its three continuation prompts during
// one long quiet build, recovered and worked for another 20 minutes, and was
// then killed instantly on the very next short stall. Real progress must
// give a later stall a fresh per-stall budget.
func TestProgressWatchdogResetsNudgeBudgetOnProgress(t *testing.T) {
	start := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	now := start
	w := &progressWatchdog{
		now:          func() time.Time { return now },
		lastProgress: start,
		nudge:        func(context.Context) error { return nil },
	}

	// Three separate stalls, each ending in a nudge followed by real
	// progress — the exact incident pattern.
	for stall := 0; stall < 3; stall++ {
		now = now.Add(harnessProgressNudgeAfter)
		nudge, cause := w.check(now)
		if !nudge || cause != stuckCauseNone {
			t.Fatalf("stall %d = nudge:%v cause:%v, want nudge only", stall+1, nudge, cause)
		}
		now = now.Add(time.Second)
		w.record("opencode: message.part.updated")
	}

	// A fourth stall must still get a nudge: the per-stall budget reset.
	now = now.Add(harnessProgressNudgeAfter)
	nudge, cause := w.check(now)
	if !nudge || cause != stuckCauseNone {
		t.Fatalf("stall after repeated recovery = nudge:%v cause:%v, want nudge only", nudge, cause)
	}
	if w.nudgeCount != 1 {
		t.Fatalf("nudgeCount = %d, want 1 (per-stall budget reset on progress)", w.nudgeCount)
	}
}

func TestProgressWatchdogCapsTotalContinuationAttempts(t *testing.T) {
	start := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	now := start
	w := &progressWatchdog{
		now:          func() time.Time { return now },
		lastProgress: start,
		nudge:        func(context.Context) error { return nil },
	}

	// A harness that only moves when prodded: each nudge produces a single
	// event, then silence again. Per-stall budgets keep resetting, so the
	// absolute total cap is the only thing that eventually stops it.
	for attempt := 0; attempt < maxHarnessContinuationAttemptsTotal; attempt++ {
		now = now.Add(harnessProgressNudgeAfter)
		nudge, cause := w.check(now)
		if !nudge || cause != stuckCauseNone {
			t.Fatalf("attempt %d = nudge:%v cause:%v, want nudge only", attempt+1, nudge, cause)
		}
		now = now.Add(time.Second)
		w.record("opencode: message.part.updated")
	}

	now = now.Add(harnessProgressNudgeAfter)
	nudge, cause := w.check(now)
	if nudge || cause != stuckCauseContinuationAttemptsExhausted {
		t.Fatalf("total attempt cap = nudge:%v cause:%v, want attempts-exhausted fail only", nudge, cause)
	}
}

func TestProgressWatchdogBusyProbeExtendsSilence(t *testing.T) {
	start := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	now := start
	w := &progressWatchdog{
		now:          func() time.Time { return now },
		lastProgress: start,
		nudge:        func(context.Context) error { t.Fatal("must not nudge a busy harness"); return nil },
		probe:        func(context.Context) (string, error) { return "busy", nil },
	}

	// The normal nudge window passes with no nudge: an in-flight turn.
	_, cause := w.check(start.Add(harnessProgressNudgeAfter))
	if cause != stuckCauseNone {
		t.Fatalf("busy harness at nudge window = cause:%v, want none", cause)
	}

	// Past the extended busy window the in-flight request is declared hung.
	now = start.Add(harnessProgressBusyFailAfter)
	_, cause = w.check(now)
	if cause != stuckCauseBusyUnresponsive {
		t.Fatalf("busy harness at extended window = cause:%v, want busy-unresponsive", cause)
	}
}

func TestProgressWatchdogIdleProbeKeepsNudgeFlow(t *testing.T) {
	start := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	now := start
	w := &progressWatchdog{
		now:          func() time.Time { return now },
		lastProgress: start,
		nudge:        func(context.Context) error { return nil },
		probe:        func(context.Context) (string, error) { return "idle", nil },
	}

	nudge, cause := w.check(start.Add(harnessProgressNudgeAfter))
	if !nudge || cause != stuckCauseNone {
		t.Fatalf("idle-probed silent harness = nudge:%v cause:%v, want nudge only", nudge, cause)
	}
}

func TestProgressWatchdogProbeErrorDegradesGracefully(t *testing.T) {
	start := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	now := start
	w := &progressWatchdog{
		now:          func() time.Time { return now },
		lastProgress: start,
		nudge:        func(context.Context) error { return nil },
		probe:        func(context.Context) (string, error) { return "", errors.New("connection refused") },
	}

	nudge, cause := w.check(start.Add(harnessProgressNudgeAfter))
	if !nudge || cause != stuckCauseNone {
		t.Fatalf("probe error = nudge:%v cause:%v, want nudge only", nudge, cause)
	}
}

func TestProgressWatchdogStuckMessageAndDetails(t *testing.T) {
	start := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	failed := time.Date(2026, 7, 12, 12, 7, 0, 0, time.UTC)
	w := &progressWatchdog{
		now:            func() time.Time { return failed },
		lastProgress:   start,
		nudgeCount:     1,
		nudgeTotal:     4,
		lastSummary:    "opencode: session diff",
		turnStateKnown: true,
		turnActive:     true,
	}

	w.fail(stuckCauseContinuationAttemptsExhausted)
	if msg := w.stuckMessage(); !strings.Contains(msg, "no progress") && !strings.Contains(msg, "budget exhausted") {
		t.Fatalf("stuck message missing cause: %q", msg)
	}
	if !w.isStuck() {
		t.Fatal("isStuck = false after fail")
	}

	details := w.stuckDetails()
	for _, want := range []string{"stuck harness", "silent for", "continuation prompts sent", "session diff", "in flight"} {
		if !strings.Contains(w.stuckError().Error(), want) {
			t.Fatalf("stuck error missing %q: %q", want, w.stuckError().Error())
		}
	}
	if !strings.Contains(details, "7m0s") {
		t.Fatalf("stuck details missing silence duration: %q", details)
	}

	fresh := &progressWatchdog{}
	if err := fresh.stuckError(); err == nil {
		t.Fatal("stuckError on non-stuck watchdog should return a generic error")
	}
	if fresh.stuckDetails() != "" {
		t.Fatal("stuckDetails on non-stuck watchdog should be empty")
	}
}

func TestProgressWatchdogStopWaitsForRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	w := startProgressWatchdog(ctx, cancel, nil, nil, nil, nil)
	w.stop()
}
