package service

import (
	"strings"
	"testing"
)

func TestSanitizeTaskEnv(t *testing.T) {
	t.Run("nil map", func(t *testing.T) {
		got := sanitizeTaskEnv(nil)
		if len(got) != 0 {
			t.Fatalf("expected empty map, got %v", got)
		}
	})
	t.Run("safe keys preserved", func(t *testing.T) {
		env := map[string]string{"PATH": "/usr/bin", "HOME": "/root"}
		got := sanitizeTaskEnv(env)
		if got["PATH"] != "/usr/bin" {
			t.Errorf("PATH not preserved")
		}
		if got["HOME"] != "/root" {
			t.Errorf("HOME not preserved")
		}
	})
	t.Run("SECRET redacted", func(t *testing.T) {
		env := map[string]string{"MY_SECRET": "supersecret"}
		got := sanitizeTaskEnv(env)
		if got["MY_SECRET"] != "[redacted]" {
			t.Errorf("MY_SECRET should be redacted, got %q", got["MY_SECRET"])
		}
	})
	t.Run("TOKEN redacted", func(t *testing.T) {
		env := map[string]string{"AUTH_TOKEN": "tok123"}
		got := sanitizeTaskEnv(env)
		if got["AUTH_TOKEN"] != "[redacted]" {
			t.Errorf("AUTH_TOKEN should be redacted, got %q", got["AUTH_TOKEN"])
		}
	})
	t.Run("KEY redacted", func(t *testing.T) {
		env := map[string]string{"API_KEY": "key123"}
		got := sanitizeTaskEnv(env)
		if got["API_KEY"] != "[redacted]" {
			t.Errorf("API_KEY should be redacted, got %q", got["API_KEY"])
		}
	})
	t.Run("PASSWORD redacted", func(t *testing.T) {
		env := map[string]string{"DB_PASSWORD": "pass123"}
		got := sanitizeTaskEnv(env)
		if got["DB_PASSWORD"] != "[redacted]" {
			t.Errorf("DB_PASSWORD should be redacted, got %q", got["DB_PASSWORD"])
		}
	})
	t.Run("case insensitive", func(t *testing.T) {
		tests := []struct{ key, want string }{
			{"secret", "[redacted]"},
			{"SECRET", "[redacted]"},
			{"Secret", "[redacted]"},
			{"MySecret", "[redacted]"},
		}
		for _, tc := range tests {
			env := map[string]string{tc.key: "val"}
			got := sanitizeTaskEnv(env)
			if got[tc.key] != tc.want {
				t.Errorf("sanitizeTaskEnv key %q = %q, want %q", tc.key, got[tc.key], tc.want)
			}
		}
	})
	t.Run("mixed safe and unsafe", func(t *testing.T) {
		env := map[string]string{
			"PATH":     "/usr/bin",
			"API_KEY":  "secretkey",
			"APP_NAME": "myapp",
			"TOKEN":    "tok",
		}
		got := sanitizeTaskEnv(env)
		if got["PATH"] != "/usr/bin" {
			t.Errorf("PATH not preserved")
		}
		if got["API_KEY"] != "[redacted]" {
			t.Errorf("API_KEY not redacted")
		}
		if got["APP_NAME"] != "myapp" {
			t.Errorf("APP_NAME not preserved")
		}
		if got["TOKEN"] != "[redacted]" {
			t.Errorf("TOKEN not redacted")
		}
	})
}

func TestRandomID(t *testing.T) {
	t.Run("task prefix", func(t *testing.T) {
		id, err := randomID("task")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(id, "task_") {
			t.Errorf("expected task_ prefix, got %q", id)
		}
		hexPart := id[len("task_"):]
		if len(hexPart) != 32 {
			t.Errorf("expected 32 hex chars after prefix, got %d", len(hexPart))
		}
	})
	t.Run("evt prefix", func(t *testing.T) {
		id, err := randomID("evt")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(id, "evt_") {
			t.Errorf("expected evt_ prefix, got %q", id)
		}
		hexPart := id[len("evt_"):]
		if len(hexPart) != 32 {
			t.Errorf("expected 32 hex chars after prefix, got %d", len(hexPart))
		}
	})
	t.Run("total length", func(t *testing.T) {
		id, _ := randomID("task")
		expectedLen := len("task") + 1 + 32
		if len(id) != expectedLen {
			t.Errorf("expected length %d, got %d", expectedLen, len(id))
		}
	})
	t.Run("only hex after prefix underscore", func(t *testing.T) {
		id, err := randomID("task")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		hexPart := id[len("task_"):]
		for _, c := range hexPart {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
				t.Errorf("non-hex char %q in %q", c, hexPart)
				break
			}
		}
	})
	t.Run("unique ids", func(t *testing.T) {
		id1, _ := randomID("task")
		id2, _ := randomID("task")
		if id1 == id2 {
			t.Fatal("consecutive randomIDs should differ")
		}
	})
	t.Run("empty prefix", func(t *testing.T) {
		id, err := randomID("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.HasPrefix(id, "_") {
			t.Errorf("expected _ prefix with empty prefix, got %q", id)
		}
		if len(id) != 1+32 {
			t.Errorf("expected length 33, got %d", len(id))
		}
	})
}
