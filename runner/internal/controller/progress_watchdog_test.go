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
