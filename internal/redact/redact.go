// Package redact provides exact-match secret redaction for runner output.
//
// It builds a transient per-execution set of known secret values derived from
// designated sensitive environment variables and credentials injected by
// managed integrations. Before any runner output is persisted or published,
// exact occurrences of those values are replaced with [REDACTED].
//
// The redaction set is never logged, audited, or persisted. It is held only
// in memory for the duration of the execution and discarded afterwards.
package redact

import "strings"

// Set is a transient collection of exact secret values to redact.
// A nil or empty Set is a no-op for Apply.
type Set struct {
	values []string
}

// NewSet extracts non-empty secret-bearing values from env. A key is
// considered sensitive when its uppercased form contains any of SECRET,
// TOKEN, KEY, or PASSWORD.
func NewSet(env map[string]string) *Set {
	if len(env) == 0 {
		return &Set{}
	}
	seen := make(map[string]bool, len(env))
	var values []string
	for key, value := range env {
		if value == "" {
			continue
		}
		upper := strings.ToUpper(key)
		if !strings.Contains(upper, "SECRET") &&
			!strings.Contains(upper, "TOKEN") &&
			!strings.Contains(upper, "KEY") &&
			!strings.Contains(upper, "PASSWORD") {
			continue
		}
		if seen[value] {
			continue
		}
		seen[value] = true
		values = append(values, value)
	}
	return &Set{values: values}
}

// Apply replaces every exact occurrence of any value in the set with
// "[REDACTED]". If s is nil or empty the original text is returned unchanged.
// The replacement is performed with a single pass using strings.ReplaceAll for
// each value. Values shorter than 4 characters are skipped to avoid false
// positives on short tokens.
func (s *Set) Apply(text string) string {
	if s == nil || len(s.values) == 0 || text == "" {
		return text
	}
	for _, v := range s.values {
		if len(v) < 4 {
			continue
		}
		text = strings.ReplaceAll(text, v, "[REDACTED]")
	}
	return text
}

// Empty reports whether the set contains no values.
func (s *Set) Empty() bool {
	return s == nil || len(s.values) == 0
}
