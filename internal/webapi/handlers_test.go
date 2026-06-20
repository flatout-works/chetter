package webapi

import (
	"testing"
	"time"

	apiv1 "github.com/flatout-works/chetter/gen/proto/api/v1"
	"github.com/flatout-works/chetter/internal/service"
)

func TestOptTimeStr(t *testing.T) {
	t.Run("nil time", func(t *testing.T) {
		got := optTimeStr(nil)
		if got != nil {
			t.Errorf("optTimeStr(nil) = %v, want nil", *got)
		}
	})

	t.Run("valid time", func(t *testing.T) {
		now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		got := optTimeStr(&now)
		if got == nil {
			t.Fatal("optTimeStr returned nil")
		}
		if *got != "2025-01-01T00:00:00Z" {
			t.Errorf("optTimeStr = %q, want %q", *got, "2025-01-01T00:00:00Z")
		}
	})
}

func TestTimeStrPtr(t *testing.T) {
	t.Run("nil time", func(t *testing.T) {
		got := timeStrPtr(nil)
		if got != "" {
			t.Errorf("timeStrPtr(nil) = %q, want empty", got)
		}
	})

	t.Run("valid time", func(t *testing.T) {
		now := time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC)
		got := timeStrPtr(&now)
		if got != "2025-06-15T10:30:00Z" {
			t.Errorf("timeStrPtr = %q, want %q", got, "2025-06-15T10:30:00Z")
		}
	})
}

func TestParseTime(t *testing.T) {
	t.Run("empty string", func(t *testing.T) {
		got := parseTime("")
		if !got.IsZero() {
			t.Errorf("parseTime('') = %v, want zero time", got)
		}
	})

	t.Run("valid RFC3339", func(t *testing.T) {
		got := parseTime("2025-01-01T00:00:00Z")
		want := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("parseTime = %v, want %v", got, want)
		}
	})

	t.Run("invalid format returns zero", func(t *testing.T) {
		got := parseTime("not-a-time")
		if !got.IsZero() {
			t.Errorf("parseTime('not-a-time') = %v, want zero", got)
		}
	})
}

func TestProtoSeverity(t *testing.T) {
	s := service.SeveritySummary{
		Critical: 1,
		High:     2,
		Medium:   3,
		Low:      4,
		Unknown:  0,
		Total:    10,
	}
	got := protoSeverity(s)
	want := &apiv1.SeveritySummary{
		Critical: 1,
		High:     2,
		Medium:   3,
		Low:      4,
		Unknown:  0,
		Total:    10,
	}
	if got.Critical != want.Critical || got.High != want.High || got.Medium != want.Medium ||
		got.Low != want.Low || got.Unknown != want.Unknown || got.Total != want.Total {
		t.Errorf("protoSeverity = %+v, want %+v", got, want)
	}
}

func TestBuildTriggerConfig(t *testing.T) {
	t.Run("empty trigger type", func(t *testing.T) {
		got := buildTriggerConfig("", "", "")
		if got != "" {
			t.Errorf("buildTriggerConfig('', '', '') = %q, want empty", got)
		}
	})

	t.Run("cron type returns empty config", func(t *testing.T) {
		got := buildTriggerConfig("cron", "org/repo", "")
		if got != "" {
			t.Errorf("buildTriggerConfig(cron) = %q, want empty", got)
		}
	})

	t.Run("pr_review with repo", func(t *testing.T) {
		got := buildTriggerConfig("pr_review", "flatout-works/chetter", "")
		want := `{"repo":"flatout-works/chetter"}`
		if got != want {
			t.Errorf("buildTriggerConfig(pr_review) = %q, want %q", got, want)
		}
	})

	t.Run("pr_review without repo returns empty", func(t *testing.T) {
		got := buildTriggerConfig("pr_review", "", "")
		if got != "" {
			t.Errorf("buildTriggerConfig(pr_review, no repo) = %q, want empty", got)
		}
	})

	t.Run("issue with repo", func(t *testing.T) {
		got := buildTriggerConfig("issue", "flatout-works/chetter", "")
		want := `{"repo":"flatout-works/chetter"}`
		if got != want {
			t.Errorf("buildTriggerConfig(issue) = %q, want %q", got, want)
		}
	})

	t.Run("issue with repo and event", func(t *testing.T) {
		got := buildTriggerConfig("issue", "flatout-works/chetter", "opened")
		want := `{"event":"opened","repo":"flatout-works/chetter"}`
		if got != want {
			t.Errorf("buildTriggerConfig(issue, event) = %q, want %q", got, want)
		}
	})

	t.Run("issue without repo returns empty", func(t *testing.T) {
		got := buildTriggerConfig("issue", "", "opened")
		if got != "" {
			t.Errorf("buildTriggerConfig(issue, no repo) = %q, want empty", got)
		}
	})
}

func TestProtoTask(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	task := service.TaskToolRecord{
		ID: "task_1", TeamID: "team_1", Status: "pending", Prompt: "do something",
		GitURL: "https://github.com/org/repo", GitRef: "main",
		AgentImage: "img:latest", Agent: "opencode", ProviderID: "p", ModelID: "m", VariantID: "v",
		Skills: []string{"go"}, Env: map[string]string{"K": "V"},
		TimeoutSec: 600, Summary: "summary", Error: "err",
		CreatedAt: now, UpdatedAt: now, StartedAt: &now, EndedAt: nil,
	}
	got := protoTask(task)
	if got.Id != "task_1" || got.Status != "pending" || got.Prompt != "do something" {
		t.Errorf("protoTask basic fields mismatch: id=%q status=%q prompt=%q", got.Id, got.Status, got.Prompt)
	}
	if got.StartedAt == nil || *got.StartedAt != "2025-01-01T00:00:00Z" {
		t.Errorf("StartedAt = %v, want 2025-01-01T00:00:00Z", got.StartedAt)
	}
	if got.EndedAt != nil {
		t.Errorf("EndedAt = %v, want nil", *got.EndedAt)
	}
	if len(got.Skills) != 1 || got.Skills[0] != "go" {
		t.Errorf("Skills = %v, want [go]", got.Skills)
	}
}

func TestProtoEvent(t *testing.T) {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	e := service.TaskEventRecord{
		ID: "evt_1", Subject: "runner:started", Status: "running",
		Payload: `{"summary":"hello"}`, CreatedAt: now,
	}
	got := protoEvent(e)
	if got.Id != "evt_1" || got.Status != "running" {
		t.Errorf("protoEvent basic fields: id=%q status=%q", got.Id, got.Status)
	}
	if got.TaskId != "" {
		t.Errorf("TaskId = %q, want empty (set by handler)", got.TaskId)
	}
}
