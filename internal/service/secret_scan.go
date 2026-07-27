package service

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
)

// SecretPattern is a named regex pattern for secret detection.
type SecretPattern struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern"`
}

// compiledPattern holds a pattern with its compiled regex.
type compiledPattern struct {
	Name  string
	Regex *regexp.Regexp
}

// RedactionRecord captures metadata about a single redaction.
type RedactionRecord struct {
	PatternName string `json:"pattern_name"`
	MatchCount  int    `json:"match_count"`
}

// SecretScanner scans and redacts secrets from text content.
type SecretScanner struct {
	patterns []compiledPattern
	mu       sync.RWMutex
}

// NewSecretScanner creates a SecretScanner from a list of patterns.
// Invalid patterns are logged and skipped.
func NewSecretScanner(patterns []SecretPattern) *SecretScanner {
	s := &SecretScanner{}
	s.compile(patterns)
	return s
}

// DefaultSecretPatterns returns the built-in set of secret detection patterns.
func DefaultSecretPatterns() []SecretPattern {
	return []SecretPattern{
		// Bearer tokens and generic auth tokens
		{Name: "bearer_token", Pattern: `(?i)bearer\s+[A-Za-z0-9\-._~+/]+=*`},
		{Name: "generic_api_key", Pattern: `(?i)(?:api[_-]?key|apikey|auth[_-]?token|access[_-]?token)\s*[:=]\s*['"]?[A-Za-z0-9\-._~+/=]{20,}['"]?`},

		// GitHub personal access tokens
		{Name: "github_pat_classic", Pattern: `ghp_[A-Za-z0-9_]{36,}`},
		{Name: "github_pat_fine_grained", Pattern: `github_pat_[A-Za-z0-9_]{22,}`},
		{Name: "github_oauth_token", Pattern: `gho_[A-Za-z0-9_]{36,}`},
		{Name: "github_user_token", Pattern: `ghu_[A-Za-z0-9_]{36,}`},
		{Name: "github_server_token", Pattern: `ghs_[A-Za-z0-9_]{36,}`},
		{Name: "github_refresh_token", Pattern: `ghr_[A-Za-z0-9_]{36,}`},

		// AWS access keys
		{Name: "aws_access_key_id", Pattern: `AKIA[0-9A-Z]{16}`},
		{Name: "aws_secret_access_key", Pattern: `(?i)aws(?:[_-]?secret)?[_-]?(?:access)?[_-]?key['\"]?\s*[:=]\s*['\"]?[A-Za-z0-9/+]{40}['\"]?`},

		// Private key blocks
		{Name: "private_key_pem", Pattern: `-----BEGIN\s+(?:RSA\s+|EC\s+|DSA\s+|OPENSSH\s+|ENCRYPTED\s+)?PRIVATE\s+KEY-----`},
		{Name: "private_key_openssh", Pattern: `-----BEGIN\s+OPENSSH\s+PRIVATE\s+KEY-----`},

		// Generic secret/token/password env var patterns
		{Name: "env_secret", Pattern: `(?i)(?:secret|token|password|passwd|credential)\s*[:=]\s*['"]?[^\s'"]{8,}['"]?`},
	}
}

// compile compiles the given patterns, logging and skipping any invalid ones.
func (s *SecretScanner) compile(patterns []SecretPattern) {
	s.mu.Lock()
	defer s.mu.Unlock()

	compiled := make([]compiledPattern, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p.Pattern)
		if err != nil {
			slog.Warn("skipping invalid secret scan pattern", "name", p.Name, "error", err)
			continue
		}
		compiled = append(compiled, compiledPattern{Name: p.Name, Regex: re})
	}
	s.patterns = compiled
}

// Redact scans the payload for secrets and replaces matches with [REDACTED].
// It returns the redacted payload and a list of redaction records.
func (s *SecretScanner) Redact(payload []byte) ([]byte, []RedactionRecord) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.patterns) == 0 {
		return payload, nil
	}

	var records []RedactionRecord
	text := string(payload)

	for _, p := range s.patterns {
		matches := p.Regex.FindAllString(text, -1)
		if len(matches) > 0 {
			// Replace each match with [REDACTED]
			text = p.Regex.ReplaceAllString(text, "[REDACTED]")
			records = append(records, RedactionRecord{
				PatternName: p.Name,
				MatchCount:  len(matches),
			})
		}
	}

	return []byte(text), records
}

// RedactString is a convenience method for scanning a plain string payload.
func (s *SecretScanner) RedactString(text string) (string, []RedactionRecord) {
	redacted, records := s.Redact([]byte(text))
	return string(redacted), records
}

// RedactJSON scans a JSON payload, redacting secrets in string values
// throughout the document, and in raw text embedded in the JSON.
func (s *SecretScanner) RedactJSON(payload json.RawMessage) (json.RawMessage, []RedactionRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.patterns) == 0 || len(payload) == 0 {
		return payload, nil, false
	}

	// Check if the whole payload has any matches
	text := string(payload)
	if !s.hasAnyMatch(text) {
		return payload, nil, false
	}

	// Try to parse as JSON object for structured redaction
	var obj any
	if err := json.Unmarshal(payload, &obj); err != nil {
		// Not valid JSON, redact as plain text
		redacted, records := s.Redact(payload)
		return json.RawMessage(redacted), records, len(records) > 0
	}

	records := s.redactRecursive(&obj)
	if len(records) == 0 {
		return payload, nil, false
	}

	redacted, err := json.Marshal(obj)
	if err != nil {
		// Fallback to string-based redaction
		redacted, records := s.Redact(payload)
		return json.RawMessage(redacted), records, len(records) > 0
	}

	return json.RawMessage(redacted), records, true
}

// hasAnyMatch checks if any pattern matches the given text.
func (s *SecretScanner) hasAnyMatch(text string) bool {
	for _, p := range s.patterns {
		if p.Regex.MatchString(text) {
			return true
		}
	}
	return false
}

// redactRecursive walks a parsed JSON value and redacts string values in place.
// Returns aggregate redaction records.
func (s *SecretScanner) redactRecursive(val *any) []RedactionRecord {
	counts := map[string]int{}

	var walk func(v any) any
	walk = func(v any) any {
		switch vv := v.(type) {
		case string:
			redacted := vv
			for _, p := range s.patterns {
				if p.Regex.MatchString(redacted) {
					matches := p.Regex.FindAllString(redacted, -1)
					counts[p.Name] += len(matches)
					redacted = p.Regex.ReplaceAllString(redacted, "[REDACTED]")
				}
			}
			return redacted
		case map[string]any:
			for k, val := range vv {
				vv[k] = walk(val)
			}
			return vv
		case []any:
			for i, item := range vv {
				vv[i] = walk(item)
			}
			return vv
		default:
			return v
		}
	}

	*val = walk(*val)

	var records []RedactionRecord
	for name, count := range counts {
		records = append(records, RedactionRecord{
			PatternName: name,
			MatchCount:  count,
		})
	}
	return records
}

// ActivePatterns returns the names of all compiled patterns (for inspection).
func (s *SecretScanner) ActivePatterns() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, len(s.patterns))
	for i, p := range s.patterns {
		names[i] = p.Name
	}
	return names
}

// ActivePatternCount returns the number of active patterns.
func (s *SecretScanner) ActivePatternCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.patterns)
}

// ParseSecretPatterns parses the SECRET_SCAN_PATTERNS env var value.
// It accepts a JSON array of {name, pattern} objects.
func ParseSecretPatterns(raw string) ([]SecretPattern, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var patterns []SecretPattern
	if err := json.Unmarshal([]byte(raw), &patterns); err != nil {
		return nil, fmt.Errorf("invalid SECRET_SCAN_PATTERNS JSON: %w", err)
	}
	// Validate required fields
	for i, p := range patterns {
		if p.Name == "" {
			return nil, fmt.Errorf("SECRET_SCAN_PATTERNS[%d]: name is required", i)
		}
		if p.Pattern == "" {
			return nil, fmt.Errorf("SECRET_SCAN_PATTERNS[%d]: pattern is required", i)
		}
	}
	return patterns, nil
}
