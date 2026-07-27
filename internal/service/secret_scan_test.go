package service

import (
	"encoding/json"
	"testing"
)

func TestDefaultSecretPatterns(t *testing.T) {
	patterns := DefaultSecretPatterns()
	if len(patterns) == 0 {
		t.Fatal("expected non-empty default patterns")
	}

	scanner := NewSecretScanner(patterns)
	if scanner.ActivePatternCount() != len(patterns) {
		t.Errorf("expected %d patterns, got %d", len(patterns), scanner.ActivePatternCount())
	}
}

func TestRedactString(t *testing.T) {
	scanner := NewSecretScanner(DefaultSecretPatterns())

	tests := []struct {
		name     string
		input    string
		wantRed  bool
		wantSubs string // substrings that should NOT appear in output
	}{
		{
			name:    "github classic PAT",
			input:   `GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz1234567890AB`,
			wantRed: true,
			wantSubs: "ghp_abcdefghijklmnopqrstuvwxyz1234567890AB",
		},
		{
			name:    "github fine-grained PAT",
			input:   `export GITHUB_TOKEN=github_pat_1234567890abcdefghijklmnop`,
			wantRed: true,
			wantSubs: "github_pat_1234567890abcdefghijklmnop",
		},
		{
			name:    "AWS access key",
			input:   `AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE`,
			wantRed: true,
			wantSubs: "AKIAIOSFODNN7EXAMPLE",
		},
		{
			name:    "bearer token",
			input:   `Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0`,
			wantRed: true,
			wantSubs: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		},
		{
			name:    "private key block",
			input:   "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA...\n-----END RSA PRIVATE KEY-----",
			wantRed: true,
			wantSubs: "BEGIN RSA PRIVATE KEY",
		},
		{
			name:    "env secret assignment",
			input:   `SECRET=super-secret-value-12345`,
			wantRed: true,
			wantSubs: "super-secret-value-12345",
		},
		{
			name:    "clean output unchanged",
			input:   `Build completed successfully. 42 tests passed.`,
			wantRed: false,
		},
		{
			name:    "URL with token-like query param",
			input:   `https://example.com/api?token=not-a-real-secret-just-short`,
			wantRed: true, // "token=" pattern should match
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redacted, records := scanner.RedactString(tt.input)
			if tt.wantRed {
				if len(records) == 0 {
					t.Error("expected redactions but got none")
				}
				for _, rec := range records {
					t.Logf("redacted: pattern=%s count=%d", rec.PatternName, rec.MatchCount)
				}
			}
			if tt.wantSubs != "" && strContains(redacted, tt.wantSubs) {
				t.Errorf("secret substring %q was not redacted in: %s", tt.wantSubs, redacted)
			}
		})
	}
}

func TestRedactJSON(t *testing.T) {
	scanner := NewSecretScanner(DefaultSecretPatterns())

	tests := []struct {
		name    string
		input   string
		wantRed bool
	}{
		{
			name: "json with github token",
			input: `{
				"status": "running",
				"output": "Setting GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz1234567890AB"
			}`,
			wantRed: true,
		},
		{
			name: "json with AWS key in nested object",
			input: `{
				"env": {
					"AWS_ACCESS_KEY_ID": "AKIAIOSFODNN7EXAMPLE"
				}
			}`,
			wantRed: true,
		},
		{
			name: "json with no secrets",
			input: `{
				"status": "running",
				"summary": "Installing dependencies..."
			}`,
			wantRed: false,
		},
		{
			name: "non-json string with secrets",
			input: `Bearer ghp_token123456789012345678901234567890`,
			wantRed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := json.RawMessage(tt.input)
			redacted, records, changed := scanner.RedactJSON(payload)
			if tt.wantRed && !changed {
				t.Error("expected redaction but got none")
			}
			if !tt.wantRed && changed {
				t.Errorf("unexpected redaction: %v", records)
			}
			if changed {
				// The redacted output should NOT contain the original secret patterns
				raw := string(redacted)
				if strContains(raw, "ghp_abcdefghijklmnopqrstuvwxyz1234567890AB") {
					t.Error("github token was not redacted from JSON")
				}
				if strContains(raw, "AKIAIOSFODNN7EXAMPLE") {
					t.Error("AWS key was not redacted from JSON")
				}
				t.Logf("redacted: %s", raw)
			}
		})
	}
}

func TestCustomPatterns(t *testing.T) {
	custom := []SecretPattern{
		{Name: "custom_api_key", Pattern: `myapp_[A-Za-z0-9]{32}`},
	}
	scanner := NewSecretScanner(custom)

	redacted, records := scanner.RedactString("Key: myapp_a1b2c3d4e5f6g7h8i9j0k1l2m3n4o5p6")
	if len(records) == 0 {
		t.Error("custom pattern should match")
	}
	if strContains(redacted, "myapp_") {
		t.Error("custom secret was not redacted")
	}
}

func TestInvalidPatternSkipped(t *testing.T) {
	patterns := []SecretPattern{
		{Name: "valid", Pattern: `test\d+`},
		{Name: "invalid", Pattern: `[invalid`},
	}
	scanner := NewSecretScanner(patterns)
	if scanner.ActivePatternCount() != 1 {
		t.Errorf("expected 1 valid pattern, got %d", scanner.ActivePatternCount())
	}
	names := scanner.ActivePatterns()
	if len(names) != 1 || names[0] != "valid" {
		t.Errorf("expected only 'valid' pattern, got %v", names)
	}
}

func TestActivePatterns(t *testing.T) {
	scanner := NewSecretScanner(DefaultSecretPatterns())
	names := scanner.ActivePatterns()
	if len(names) == 0 {
		t.Fatal("expected non-empty active patterns")
	}
	// Verify no duplicates
	seen := make(map[string]bool)
	for _, name := range names {
		if seen[name] {
			t.Errorf("duplicate pattern name: %s", name)
		}
		seen[name] = true
	}
}

func TestParseSecretPatterns(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
		wantLen int
	}{
		{name: "empty", raw: "", wantErr: false, wantLen: 0},
		{name: "whitespace", raw: "   ", wantErr: false, wantLen: 0},
		{name: "valid JSON", raw: `[{"name":"test","pattern":"foo\\d+"}]`, wantErr: false, wantLen: 1},
		{name: "missing name", raw: `[{"pattern":"foo"}]`, wantErr: true},
		{name: "missing pattern", raw: `[{"name":"test"}]`, wantErr: true},
		{name: "invalid JSON", raw: `not json`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			patterns, err := ParseSecretPatterns(tt.raw)
			if tt.wantErr && err == nil {
				t.Error("expected error but got none")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if len(patterns) != tt.wantLen {
				t.Errorf("expected %d patterns, got %d", tt.wantLen, len(patterns))
			}
		})
	}
}

func TestRedactNoPatterns(t *testing.T) {
	scanner := NewSecretScanner(nil)
	redacted, records := scanner.RedactString("ghp_abcdefghijklmnopqrstuvwxyz1234567890AB")
	if len(records) != 0 {
		t.Error("expected no redactions with nil patterns")
	}
	if redacted != "ghp_abcdefghijklmnopqrstuvwxyz1234567890AB" {
		t.Error("expected unchanged output with nil patterns")
	}
}

func strContains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
