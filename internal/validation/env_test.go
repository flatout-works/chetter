package validation

import (
	"strings"
	"testing"
)

func TestValidateTaskEnv_Empty(t *testing.T) {
	if err := ValidateTaskEnv(nil, Defaults()); err != nil {
		t.Fatalf("nil env should pass: %v", err)
	}
	if err := ValidateTaskEnv(map[string]string{}, Defaults()); err != nil {
		t.Fatalf("empty env should pass: %v", err)
	}
}

func TestValidateTaskEnv_Valid(t *testing.T) {
	env := map[string]string{
		"MY_VAR":    "value",
		"DEBUG":     "1",
		"NODE_ENV":  "production",
		"API_URL":   "https://example.com",
		"MAX_ITEMS": "100",
	}
	if err := ValidateTaskEnv(env, Defaults()); err != nil {
		t.Fatalf("valid env should pass: %v", err)
	}
}

func TestValidateTaskEnv_BlockedPrefixes(t *testing.T) {
	tests := []struct {
		key   string
		value string
	}{
		{"CHETTER_SECRET", "x"},
		{"RUNNER_MODE", "x"},
		{"MCP_AUTH_TOKEN", "x"},
		{"DATABASE_URL", "x"},
		{"GITHUB_APP_KEY", "x"},
		{"ARCANE_URL", "x"},
		{"LLM_PROVIDER", "x"},
		// Case-insensitive.
		{"chetter_foo", "x"},
		{"CheTter_bar", "x"},
	}
	for _, tt := range tests {
		env := map[string]string{tt.key: tt.value}
		err := ValidateTaskEnv(env, Defaults())
		if err == nil {
			t.Errorf("%q should be blocked by prefix", tt.key)
		}
	}
}

func TestValidateTaskEnv_BlockedNames(t *testing.T) {
	tests := []string{"PATH", "HOME", "SHELL", "LD_PRELOAD", "LD_LIBRARY_PATH", "path", "Path"}
	for _, name := range tests {
		env := map[string]string{name: "x"}
		err := ValidateTaskEnv(env, Defaults())
		if err == nil {
			t.Errorf("%q should be blocked as exact name match", name)
		}
	}
}

func TestValidateTaskEnv_MaxCount(t *testing.T) {
	cfg := Defaults()
	cfg.MaxCount = 5
	env := map[string]string{
		"A": "1", "B": "2", "C": "3", "D": "4", "E": "5", "F": "6",
	}
	err := ValidateTaskEnv(env, cfg)
	if err == nil {
		t.Fatal("should reject env with more than 5 vars")
	}
	if !strings.Contains(err.Error(), "too many environment variables") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTaskEnv_MaxNameLength(t *testing.T) {
	cfg := Defaults()
	cfg.MaxNameLength = 10
	longName := strings.Repeat("X", 11)
	env := map[string]string{longName: "value"}
	err := ValidateTaskEnv(env, cfg)
	if err == nil {
		t.Fatal("should reject long env var name")
	}
	if !strings.Contains(err.Error(), "name too long") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTaskEnv_MaxValueLength(t *testing.T) {
	cfg := Defaults()
	cfg.MaxValueLength = 10
	env := map[string]string{"MY_VAR": strings.Repeat("X", 11)}
	err := ValidateTaskEnv(env, cfg)
	if err == nil {
		t.Fatal("should reject long env var value")
	}
	if !strings.Contains(err.Error(), "value too long") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateTaskEnv_MultipleErrors(t *testing.T) {
	cfg := Defaults()
	cfg.MaxNameLength = 5
	cfg.MaxValueLength = 5
	env := map[string]string{
		"VERY_LONG_NAME":  "value",
		"OK":              strings.Repeat("X", 10),
		"CHETTER_SECRET":  "x",
		"PATH":            "x",
	}
	err := ValidateTaskEnv(env, cfg)
	if err == nil {
		t.Fatal("should return multiple errors")
	}
	msg := err.Error()
	if !strings.Contains(msg, "name too long") {
		t.Error("missing name too long error")
	}
	if !strings.Contains(msg, "value too long") {
		t.Error("missing value too long error")
	}
	if !strings.Contains(msg, "CHETTER_SECRET") {
		t.Error("missing blocked prefix error")
	}
	if !strings.Contains(msg, "PATH") {
		t.Error("missing blocked name error")
	}
}

func TestValidateTaskEnv_CustomConfig(t *testing.T) {
	cfg := Config{
		BlockedPrefixes: []string{"CUSTOM_"},
		BlockedNames:    []string{"FOO"},
		MaxCount:        10,
		MaxNameLength:   50,
		MaxValueLength:  100,
	}
	// Should block CUSTOM_ prefix.
	if err := ValidateTaskEnv(map[string]string{"CUSTOM_VAR": "x"}, cfg); err == nil {
		t.Error("should block CUSTOM_ prefix")
	}
	// Should block FOO exactly.
	if err := ValidateTaskEnv(map[string]string{"FOO": "x"}, cfg); err == nil {
		t.Error("should block exact name FOO")
	}
	// Should NOT block standard prefix when not configured.
	if err := ValidateTaskEnv(map[string]string{"CHETTER_OK": "x"}, cfg); err != nil {
		t.Errorf("should not block CHETTER_ prefix with custom config: %v", err)
	}
}

func TestValidateTaskEnv_CountAtLimit(t *testing.T) {
	cfg := Defaults()
	cfg.MaxCount = 3
	env := map[string]string{"A": "1", "B": "2", "C": "3"}
	if err := ValidateTaskEnv(env, cfg); err != nil {
		t.Fatalf("exactly-at-limit should pass: %v", err)
	}
}

func TestValidateTaskEnv_CountExceededShortCircuits(t *testing.T) {
	// When count is exceeded, we should return early and not enumerate
	// individual name/value violations.
	cfg := Defaults()
	cfg.MaxCount = 1
	cfg.MaxNameLength = 1
	env := map[string]string{
		"VERY_LONG_NAME": "x",
		"ANOTHER":        "x",
	}
	err := ValidateTaskEnv(env, cfg)
	if err == nil {
		t.Fatal("should reject")
	}
	msg := err.Error()
	if strings.Contains(msg, "name too long") {
		t.Error("count violation should short-circuit; should not contain name length errors")
	}
	if !strings.Contains(msg, "too many environment variables") {
		t.Fatalf("expected count error, got: %v", err)
	}
}
