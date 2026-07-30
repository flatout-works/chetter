package validation

import (
	"strings"
	"testing"
)

func TestValidateTaskInput_SupportedHarnesses(t *testing.T) {
	limits := DefaultTaskLimits()
	// Every supported harness (including empty = default) must pass.
	for _, h := range append([]string{""}, SupportedHarnesses...) {
		if err := ValidateTaskInput(TaskInput{Harness: h}, limits); err != nil {
			t.Errorf("harness %q should be accepted: %v", h, err)
		}
	}
}

func TestValidateTaskInput_UnknownHarnessRejected(t *testing.T) {
	limits := DefaultTaskLimits()
	for _, h := range []string{"OpenCode", "opencode2", "cursor", "aider", "claude"} {
		err := ValidateTaskInput(TaskInput{Harness: h}, limits)
		if err == nil {
			t.Errorf("harness %q should be rejected", h)
			continue
		}
		if !strings.Contains(err.Error(), "harness") {
			t.Errorf("harness %q error should mention field: %v", h, err)
		}
	}
}

func TestValidateTaskInput_SessionModes(t *testing.T) {
	limits := DefaultTaskLimits()
	for _, m := range []string{"", "none", "resumable"} {
		if err := ValidateTaskInput(TaskInput{SessionMode: m}, limits); err != nil {
			t.Errorf("session_mode %q should be accepted: %v", m, err)
		}
	}
	for _, m := range []string{"Resumable", "resume", "persistent", "checkpoint"} {
		err := ValidateTaskInput(TaskInput{SessionMode: m}, limits)
		if err == nil {
			t.Errorf("session_mode %q should be rejected", m)
		}
	}
}

func TestValidateTaskInput_TimeoutBounds(t *testing.T) {
	limits := DefaultTaskLimits() // MaxTimeoutSec = 86400
	cases := []struct {
		name    string
		timeout int
		ok      bool
	}{
		{"zero defaults later", 0, true},
		{"positive within limit", 600, true},
		{"exactly at limit", limits.MaxTimeoutSec, true},
		{"just over limit", limits.MaxTimeoutSec + 1, false},
		{"negative", -1, false},
	}
	for _, tc := range cases {
		err := ValidateTaskInput(TaskInput{TimeoutSec: tc.timeout}, limits)
		if tc.ok && err != nil {
			t.Errorf("%s: expected ok, got %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestValidateTaskInput_TTLBounds(t *testing.T) {
	limits := DefaultTaskLimits() // MaxTTLHours = 720
	cases := []struct {
		name string
		ttl  int
		ok   bool
	}{
		{"zero defaults later", 0, true},
		{"positive within limit", 72, true},
		{"exactly at limit", limits.MaxTTLHours, true},
		{"just over limit", limits.MaxTTLHours + 1, false},
		{"negative", -5, false},
	}
	for _, tc := range cases {
		err := ValidateTaskInput(TaskInput{TTLHours: tc.ttl}, limits)
		if tc.ok && err != nil {
			t.Errorf("%s: expected ok, got %v", tc.name, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%s: expected error, got nil", tc.name)
		}
	}
}

func TestValidateTaskInput_NoCapWhenLimitsZero(t *testing.T) {
	// A zero-value TaskLimits disables the upper-bound checks but still
	// rejects negative values and unsupported harnesses.
	var limits TaskLimits
	if err := ValidateTaskInput(TaskInput{TimeoutSec: 1_000_000, TTLHours: 1_000_000}, limits); err != nil {
		t.Fatalf("zero limits should allow large positive values: %v", err)
	}
	if err := ValidateTaskInput(TaskInput{TimeoutSec: -1}, limits); err == nil {
		t.Fatal("negative timeout should still be rejected with zero limits")
	}
}

func TestValidateTaskInput_PauseReasonLength(t *testing.T) {
	limits := DefaultTaskLimits()
	if err := ValidateTaskInput(TaskInput{PauseReason: strings.Repeat("x", maxPauseReasonLen)}, limits); err != nil {
		t.Fatalf("pause_reason at limit should pass: %v", err)
	}
	err := ValidateTaskInput(TaskInput{PauseReason: strings.Repeat("x", maxPauseReasonLen+1)}, limits)
	if err == nil || !strings.Contains(err.Error(), "pause_reason") {
		t.Fatalf("over-limit pause_reason should fail with field name, got %v", err)
	}
}

func TestValidateTaskInput_MultipleErrorsIdentifyFields(t *testing.T) {
	limits := DefaultTaskLimits()
	err := ValidateTaskInput(TaskInput{
		Harness:    "bogus",
		SessionMode: "nope",
		TimeoutSec: -1,
		TTLHours:   -2,
	}, limits)
	if err == nil {
		t.Fatal("expected multiple validation errors")
	}
	msg := err.Error()
	for _, want := range []string{"harness", "session_mode", "timeout_sec", "ttl_hours"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error should identify field %q: %v", want, err)
		}
	}
}

func TestValidateTaskInput_DoesNotExposeSecrets(t *testing.T) {
	// Validation operates only on structural fields; env/secrets are handled
	// elsewhere. Confirm a prompt-like secret passed in pause_reason is never
	// echoed back beyond its own (bounded) length.
	limits := DefaultTaskLimits()
	secret := "ghp_supersecrettoken123"
	err := ValidateTaskInput(TaskInput{Harness: "bogus", PauseReason: secret}, limits)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("validation error must not echo free-text input: %v", err)
	}
}

func TestValidateTriggerInput_SupportedHarnessesAndTypes(t *testing.T) {
	limits := DefaultTaskLimits()
	for _, h := range append([]string{""}, SupportedHarnesses...) {
		for _, tt := range TriggerTypes {
			in := TriggerInput{TriggerType: tt, Harness: h}
			// pr_review/issue need a valid repo to pass; supply one.
			if IsGitHubTriggerType(tt) {
				in.Repo = "flatout-works/chetter"
			}
			if err := ValidateTriggerInput(in, limits); err != nil {
				t.Errorf("trigger type %q harness %q should be accepted: %v", tt, h, err)
			}
		}
	}
}

func TestValidateTriggerInput_UnknownHarness(t *testing.T) {
	limits := DefaultTaskLimits()
	err := ValidateTriggerInput(TriggerInput{TriggerType: "cron", Harness: "bogus"}, limits)
	if err == nil || !strings.Contains(err.Error(), "harness") {
		t.Fatalf("expected harness error, got %v", err)
	}
}

func TestValidateTriggerInput_UnknownTriggerType(t *testing.T) {
	limits := DefaultTaskLimits()
	err := ValidateTriggerInput(TriggerInput{TriggerType: "webhook"}, limits)
	if err == nil || !strings.Contains(err.Error(), "trigger_type") {
		t.Fatalf("expected trigger_type error, got %v", err)
	}
}

func TestValidateTriggerInput_RepoSyntax(t *testing.T) {
	limits := DefaultTaskLimits()
	valid := []string{"flatout-works/chetter", "a/b", "my-org_my.repo/v1.2.3", "  owner/repo  "}
	invalid := []string{
		"https://github.com/flatout-works/chetter",
		"git@github.com:flatout-works/chetter.git",
		"flatout-works",       // missing repo
		"/flatout-works/chetter",
		"flatout-works/chetter/",
		"flatout-works/chetter/extra",
		"flatout works/chetter", // interior space
		"flatout-works/",        // empty repo
		"/repo",                 // empty owner
		"../chetter",            // dot-only owner
	}
	// An empty repo is a required-field error (handled by the service), not a
	// syntax error, so ValidateTriggerInput ignores it.

	for _, r := range valid {
		err := ValidateTriggerInput(TriggerInput{TriggerType: "pr_review", Repo: r}, limits)
		if err != nil {
			t.Errorf("repo %q should be valid: %v", r, err)
		}
	}
	for _, r := range invalid {
		err := ValidateTriggerInput(TriggerInput{TriggerType: "issue", Repo: r}, limits)
		if err == nil {
			t.Errorf("repo %q should be rejected", r)
		}
	}
}

func TestValidateTriggerInput_CronRepoIgnored(t *testing.T) {
	limits := DefaultTaskLimits()
	// cron triggers do not carry a repo; even a bogus repo is ignored.
	if err := ValidateTriggerInput(TriggerInput{TriggerType: "cron", Repo: "not a repo"}, limits); err != nil {
		t.Fatalf("cron trigger should ignore repo: %v", err)
	}
}

func TestValidOwnerRepo(t *testing.T) {
	good := []string{"flatout-works/chetter", "a/b", "x.y_z-1/r", "  owner/repo  ", "a/b "}
	bad := []string{"", "a", "a/b/c", "a//b", "a/ b"}
	for _, s := range good {
		if !ValidOwnerRepo(s) {
			t.Errorf("ValidOwnerRepo(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if ValidOwnerRepo(s) {
			t.Errorf("ValidOwnerRepo(%q) = true, want false", s)
		}
	}
}

func TestValidationError_ErrorFormat(t *testing.T) {
	ve := ValidationError{Field: "harness", Message: "unsupported"}
	if got := ve.Error(); got != "harness: unsupported" {
		t.Errorf("Error() = %q, want %q", got, "harness: unsupported")
	}
}
