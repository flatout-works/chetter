package validation

import (
	"fmt"
	"strings"
)

// SupportedHarnesses is the set of runner harnesses Chetter can dispatch a
// task to. An empty harness means "use the runner default" and is always
// allowed; only non-empty, unrecognized values are rejected so an unknown
// harness never silently falls back to OpenCode. The set mirrors the runner's
// own execution.harness validation (see runner/internal/config).
var SupportedHarnesses = []string{
	"opencode",
	"claude-code",
	"pi",
	"codewhale",
	"codex",
}

// SupportedSessionModes is the set of accepted session_mode values. An empty
// value is equivalent to "none" (the default).
var SupportedSessionModes = []string{"", "none", "resumable"}

// TriggerTypes is the set of recognized trigger types.
var TriggerTypes = []string{"cron", "pr_review", "issue"}

// maxPauseReasonLen bounds the pause_reason free-text field.
const maxPauseReasonLen = 1024

// TaskLimits holds documented upper bounds for task/trigger runtime fields.
// A zero value means "no explicit cap" for that field, so a deployment that
// does not configure limits only rejects negative/unsupported values.
type TaskLimits struct {
	// MaxTimeoutSec caps a single task's timeout in seconds. Zero disables
	// the upper-bound check.
	MaxTimeoutSec int
	// MaxTTLHours caps a resumable session's TTL in hours. Zero disables the
	// upper-bound check.
	MaxTTLHours int
}

// DefaultTaskLimits returns documented default limits: a 24h task timeout cap
// and a 30-day resumable session TTL cap.
func DefaultTaskLimits() TaskLimits {
	return TaskLimits{
		MaxTimeoutSec: 24 * 3600,
		MaxTTLHours:   24 * 30,
	}
}

// TaskInput is the validation-relevant subset of a task submission. It is
// intentionally small so the validation package stays free of service/store
// imports; callers map their request structs onto it.
type TaskInput struct {
	Harness     string
	SessionMode string
	PauseReason string
	TTLHours    int
	TimeoutSec  int
}

// ValidateTaskInput checks the deterministic fields of a task submission. It
// returns nil when the input is acceptable, or an error identifying the
// invalid field. It never inspects secrets: env vars and tokens are out of
// scope (env is handled by ValidateTaskEnv).
func ValidateTaskInput(in TaskInput, limits TaskLimits) error {
	return joinErrors(validateTaskFields(in, limits), "task validation failed")
}

func validateTaskFields(in TaskInput, limits TaskLimits) []ValidationError {
	var errs []ValidationError

	if in.Harness != "" && !contains(SupportedHarnesses, in.Harness) {
		errs = append(errs, ValidationError{
			Field:   "harness",
			Message: fmt.Sprintf("unsupported harness %q (supported: %s)", in.Harness, joinQuoted(SupportedHarnesses)),
		})
	}

	mode := strings.TrimSpace(in.SessionMode)
	if mode != "" && mode != "none" && mode != "resumable" {
		errs = append(errs, ValidationError{
			Field:   "session_mode",
			Message: fmt.Sprintf("unsupported session_mode %q (supported: none, resumable)", in.SessionMode),
		})
	}

	if in.TimeoutSec < 0 {
		errs = append(errs, ValidationError{
			Field:   "timeout_sec",
			Message: fmt.Sprintf("timeout_sec must be non-negative, got %d", in.TimeoutSec),
		})
	} else if limits.MaxTimeoutSec > 0 && in.TimeoutSec > limits.MaxTimeoutSec {
		errs = append(errs, ValidationError{
			Field:   "timeout_sec",
			Message: fmt.Sprintf("timeout_sec %d exceeds maximum %d", in.TimeoutSec, limits.MaxTimeoutSec),
		})
	}

	if in.TTLHours < 0 {
		errs = append(errs, ValidationError{
			Field:   "ttl_hours",
			Message: fmt.Sprintf("ttl_hours must be non-negative, got %d", in.TTLHours),
		})
	} else if limits.MaxTTLHours > 0 && in.TTLHours > limits.MaxTTLHours {
		errs = append(errs, ValidationError{
			Field:   "ttl_hours",
			Message: fmt.Sprintf("ttl_hours %d exceeds maximum %d", in.TTLHours, limits.MaxTTLHours),
		})
	}

	if len(in.PauseReason) > maxPauseReasonLen {
		errs = append(errs, ValidationError{
			Field:   "pause_reason",
			Message: fmt.Sprintf("pause_reason too long: %d characters (max %d)", len(in.PauseReason), maxPauseReasonLen),
		})
	}

	return errs
}

// TriggerInput is the validation-relevant subset of a trigger create/update.
type TriggerInput struct {
	TriggerType string
	Harness     string
	SessionMode string
	PauseReason string
	TTLHours    int
	TimeoutSec  int
	// Repo is the canonical owner/repo watched by GitHub triggers. It is only
	// validated for pr_review and issue triggers; cron triggers ignore it.
	Repo string
}

// ValidateTriggerInput checks the deterministic fields of a trigger. It does
// not duplicate trigger-type-specific required-field checks (cron_expr, repo
// presence) that the service already enforces; it focuses on shared runtime
// fields and repo syntax so the same rules apply to every ingress path.
func ValidateTriggerInput(in TriggerInput, limits TaskLimits) error {
	var errs []ValidationError

	if in.TriggerType != "" && !contains(TriggerTypes, in.TriggerType) {
		errs = append(errs, ValidationError{
			Field:   "trigger_type",
			Message: fmt.Sprintf("unsupported trigger_type %q (supported: %s)", in.TriggerType, joinQuoted(TriggerTypes)),
		})
	}

	errs = append(errs, validateTaskFields(TaskInput{
		Harness:     in.Harness,
		SessionMode: in.SessionMode,
		PauseReason: in.PauseReason,
		TTLHours:    in.TTLHours,
		TimeoutSec:  in.TimeoutSec,
	}, limits)...)

	if IsGitHubTriggerType(in.TriggerType) && in.Repo != "" && !ValidOwnerRepo(in.Repo) {
		errs = append(errs, ValidationError{
			Field:   "repo",
			Message: fmt.Sprintf("repo %q is not canonical owner/repo syntax (e.g. flatout-works/chetter)", in.Repo),
		})
	}

	return joinErrors(errs, "trigger validation failed")
}

// IsGitHubTriggerType reports whether the trigger type carries an owner/repo
// in its config (pr_review or issue).
func IsGitHubTriggerType(t string) bool {
	return t == "pr_review" || t == "issue"
}

// ValidOwnerRepo reports whether s is a canonical GitHub owner/repo string
// such as "flatout-works/chetter". It rejects URLs, leading/trailing slashes,
// empty segments, additional path components, and disallowed characters.
func ValidOwnerRepo(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	parts := strings.Split(s, "/")
	if len(parts) != 2 {
		return false
	}
	owner, repo := parts[0], parts[1]
	return isOwnerRepoSegment(owner) && isOwnerRepoSegment(repo)
}

// isOwnerRepoSegment validates a single owner or repo segment per GitHub's
// naming rules: alphanumeric, hyphen, underscore, and dot, with a sane length
// bound. Segments consisting only of dots (".", "..") are rejected.
func isOwnerRepoSegment(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return strings.ReplaceAll(s, ".", "") != ""
}
func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func joinQuoted(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return strings.Join(quoted, ", ")
}
