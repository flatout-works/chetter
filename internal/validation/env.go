// Package validation provides server-side validation for task inputs.
package validation

import (
	"fmt"
	"regexp"
	"strings"
)

// envNamePattern matches a portable environment variable name: a non-empty
// POSIX identifier. Names that contain "=", NUL, whitespace, or control
// characters never match.
var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// IsValidEnvName reports whether name is a well-formed environment variable
// name (matching [A-Za-z_][A-Za-z0-9_]*).
//
// This matters for security: the runner hands task env to Docker as
// `-e "<key>=<value>"`, and Docker splits that argument at the first "=". A
// name like "PATH=/evil" therefore reaches the container as the managed name
// PATH and bypasses the blocked-name list and IsManagedEnv. Rejecting any
// name that is not a plain identifier closes that gap and also turns empty or
// malformed names into a clear submission-time validation error instead of an
// opaque container/pod failure.
func IsValidEnvName(name string) bool {
	return envNamePattern.MatchString(name)
}

// Config holds limits and blocked patterns for task environment variable
// validation. All fields are populated from server configuration at startup.
type Config struct {
	// BlockedPrefixes are name prefixes that are rejected. The check is
	// case-insensitive against the uppercased env var name.
	BlockedPrefixes []string
	// BlockedNames are exact names that are rejected. The check is
	// case-insensitive against the uppercased env var name.
	BlockedNames []string
	// MaxCount is the maximum number of env vars allowed (default 64).
	MaxCount int
	// MaxNameLength is the maximum length of an env var name (default 256).
	MaxNameLength int
	// MaxValueLength is the maximum length of an env var value (default 4096).
	MaxValueLength int
}

// Defaults returns a Config populated with safe defaults.
func Defaults() Config {
	return Config{
		BlockedPrefixes: []string{
			"CHETTER_",
			"RUNNER_",
			"MCP_AUTH",
			"DATABASE_",
			"GITHUB_APP_",
			"ARCANE_",
			"LLM_",
		},
		BlockedNames: []string{
			"PATH",
			"HOME",
			"SHELL",
			"LD_PRELOAD",
			"LD_LIBRARY_PATH",
		},
		MaxCount:      64,
		MaxNameLength: 256,
		MaxValueLength: 4096,
	}
}

// ValidationError describes a single env var validation failure.
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// ValidateTaskEnv checks task environment variables against the limits and
// blocked patterns in cfg. It returns nil if all vars pass, or an error
// wrapping all violations.
func ValidateTaskEnv(env map[string]string, cfg Config) error {
	if len(env) == 0 {
		return nil
	}
	var errs []ValidationError

	// Count limit.
	if len(env) > cfg.MaxCount {
		errs = append(errs, ValidationError{
			Field:   "env",
			Message: fmt.Sprintf("too many environment variables: %d (max %d)", len(env), cfg.MaxCount),
		})
		// Early exit: if count is already violated, name/value checks on every
		// var are noisy and unnecessary.
		return joinErrors(errs, "environment variable validation failed")
	}

	for key, value := range env {
		// Validate the raw name shape before any blocklist comparison. Docker
		// splits -e "<key>=<value>" at the first "=", so a name such as
		// "PATH=/evil" would otherwise be interpreted as PATH and bypass the
		// exact-name blocklist. Skipping the remaining checks for an invalid
		// name avoids redundant/confusing errors for the same key.
		if !IsValidEnvName(key) {
			errs = append(errs, ValidationError{
				Field:   "env[" + key + "]",
				Message: fmt.Sprintf("invalid environment variable name %q", key),
			})
			continue
		}

		upper := strings.ToUpper(strings.TrimSpace(key))

		// Name length limit.
		if len(key) > cfg.MaxNameLength {
			errs = append(errs, ValidationError{
				Field:   "env[" + key + "]",
				Message: fmt.Sprintf("name too long: %d characters (max %d)", len(key), cfg.MaxNameLength),
			})
		}

		// Value length limit.
		if len(value) > cfg.MaxValueLength {
			errs = append(errs, ValidationError{
				Field:   "env[" + key + "]",
				Message: fmt.Sprintf("value too long: %d characters (max %d)", len(value), cfg.MaxValueLength),
			})
		}

		// Blocked exact names.
		for _, blocked := range cfg.BlockedNames {
			if upper == strings.ToUpper(blocked) {
				errs = append(errs, ValidationError{
					Field:   "env[" + key + "]",
					Message: fmt.Sprintf("blocked environment variable name %q", key),
				})
				break
			}
		}

		// Blocked prefixes.
		for _, prefix := range cfg.BlockedPrefixes {
			if strings.HasPrefix(upper, strings.ToUpper(prefix)) {
				errs = append(errs, ValidationError{
					Field:   "env[" + key + "]",
					Message: fmt.Sprintf("blocked environment variable prefix: %q starts with reserved prefix %q", key, prefix),
				})
				break
			}
		}
	}

	return joinErrors(errs, "environment variable validation failed")
}

// joinErrors collapses a slice of ValidationError into a single error. A single
// violation is returned directly so callers can type-assert to ValidationError;
// multiple violations are joined under label.
func joinErrors(errs []ValidationError, label string) error {
	if len(errs) == 0 {
		return nil
	}
	if len(errs) == 1 {
		return errs[0]
	}
	var msgs []string
	for _, e := range errs {
		msgs = append(msgs, e.Error())
	}
	return fmt.Errorf("%s: %s", label, strings.Join(msgs, "; "))
}
