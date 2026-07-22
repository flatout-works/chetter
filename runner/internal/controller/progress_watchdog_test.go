package controller

import (
	"context"
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

	nudge, fail := w.check(start.Add(harnessProgressNudgeAfter))
	if !nudge || fail {
		t.Fatalf("first silent interval = nudge:%v fail:%v, want nudge only", nudge, fail)
	}

	nudge, fail = w.check(start.Add(harnessProgressNudgeAfter + harnessProgressFailAfter))
	if nudge || !fail {
		t.Fatalf("post-nudge silent interval = nudge:%v fail:%v, want fail only", nudge, fail)
	}

	now = start.Add(harnessProgressNudgeAfter + time.Second)
	w.record("opencode: message.part.updated")
	nudge, fail = w.check(now.Add(harnessProgressNudgeAfter))
	if !nudge || fail {
		t.Fatalf("progress after nudge should reset watchdog, got nudge:%v fail:%v", nudge, fail)
	}
}

func TestProgressWatchdogFailsWithoutNudge(t *testing.T) {
	start := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	w := &progressWatchdog{lastProgress: start}

	nudge, fail := w.check(start.Add(harnessProgressNudgeAfter))
	if nudge || fail {
		t.Fatalf("first silent interval = nudge:%v fail:%v, want neither", nudge, fail)
	}

	nudge, fail = w.check(start.Add(harnessProgressFailAfter))
	if nudge || !fail {
		t.Fatalf("no-nudge timeout = nudge:%v fail:%v, want fail only", nudge, fail)
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

	nudge, fail := w.check(start.Add(harnessProgressNudgeAfter))
	if nudge || fail {
		t.Fatalf("idle watchdog = nudge:%v fail:%v, want neither", nudge, fail)
	}

	nudge, fail = w.check(start.Add(harnessProgressNudgeAfter + harnessProgressFailAfter))
	if nudge || fail {
		t.Fatalf("idle watchdog at fail threshold = nudge:%v fail:%v, want neither", nudge, fail)
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

	nudge, fail := w.check(start.Add(harnessProgressNudgeAfter))
	if !nudge || fail {
		t.Fatalf("not-idle watchdog = nudge:%v fail:%v, want nudge only", nudge, fail)
	}
}

func TestProgressWatchdogCapsContinuationAttempts(t *testing.T) {
	start := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	now := start
	w := &progressWatchdog{
		now:          func() time.Time { return now },
		lastProgress: start,
		nudge:        func(context.Context) error { return nil },
	}

	for attempt := 0; attempt < maxHarnessContinuationAttempts; attempt++ {
		now = start.Add(time.Duration(attempt+1) * harnessProgressNudgeAfter)
		nudge, fail := w.check(now)
		if !nudge || fail {
			t.Fatalf("attempt %d = nudge:%v fail:%v, want nudge only", attempt+1, nudge, fail)
		}
		w.record("opencode: message.part.updated")
	}

	now = now.Add(harnessProgressNudgeAfter)
	nudge, fail := w.check(now)
	if nudge || !fail {
		t.Fatalf("attempt cap = nudge:%v fail:%v, want fail only", nudge, fail)
	}
}
