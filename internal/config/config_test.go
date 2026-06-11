package config

import (
	"testing"
)

func TestValidate(t *testing.T) {
	t.Run("all required fields present", func(t *testing.T) {
		cfg := Config{
			DatabaseDSN:  "root@tcp(localhost:4000)/db",
			TaskSubject:  "devfleet.runner.tasks",
			EventSubject: "devfleet.tasks.>",
		}
		if err := cfg.Validate(); err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})
	t.Run("missing DatabaseDSN", func(t *testing.T) {
		cfg := Config{TaskSubject: "t", EventSubject: "e"}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "DATABASE_DSN is required" {
			t.Errorf("expected DATABASE_DSN error, got %q", err.Error())
		}
	})
	t.Run("missing TaskSubject", func(t *testing.T) {
		cfg := Config{DatabaseDSN: "dsn", EventSubject: "e"}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "TASK_SUBJECT is required" {
			t.Errorf("expected TASK_SUBJECT error, got %q", err.Error())
		}
	})
	t.Run("missing EventSubject", func(t *testing.T) {
		cfg := Config{DatabaseDSN: "dsn", TaskSubject: "t"}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "EVENT_SUBJECT is required" {
			t.Errorf("expected EVENT_SUBJECT error, got %q", err.Error())
		}
	})
	t.Run("all missing", func(t *testing.T) {
		cfg := Config{}
		err := cfg.Validate()
		if err == nil {
			t.Fatal("expected error")
		}
		if err.Error() != "DATABASE_DSN is required" {
			t.Errorf("expected DATABASE_DSN as first error, got %q", err.Error())
		}
	})
}

func TestEnv(t *testing.T) {
	t.Run("env var set", func(t *testing.T) {
		t.Setenv("TEST_ENV_KEY", "myvalue")
		got := env("TEST_ENV_KEY", "fallback")
		if got != "myvalue" {
			t.Errorf("expected myvalue, got %q", got)
		}
	})
	t.Run("env var not set", func(t *testing.T) {
		got := env("TEST_ENV_MISSING_XYZ", "fallback")
		if got != "fallback" {
			t.Errorf("expected fallback, got %q", got)
		}
	})
}

func TestEnvBool(t *testing.T) {
	t.Run("true", func(t *testing.T) {
		t.Setenv("TEST_BOOL_KEY", "true")
		got := envBool("TEST_BOOL_KEY", false)
		if got != true {
			t.Errorf("expected true, got %v", got)
		}
	})
	t.Run("false", func(t *testing.T) {
		t.Setenv("TEST_BOOL_KEY2", "false")
		got := envBool("TEST_BOOL_KEY2", true)
		if got != false {
			t.Errorf("expected false, got %v", got)
		}
	})
	t.Run("not set returns fallback", func(t *testing.T) {
		got := envBool("TEST_BOOL_MISSING_XYZ", true)
		if got != true {
			t.Errorf("expected fallback true, got %v", got)
		}
	})
	t.Run("invalid value returns fallback", func(t *testing.T) {
		t.Setenv("TEST_BOOL_KEY3", "notabool")
		got := envBool("TEST_BOOL_KEY3", false)
		if got != false {
			t.Errorf("expected fallback false for invalid, got %v", got)
		}
	})
}

func TestEnvInt(t *testing.T) {
	t.Run("valid integer", func(t *testing.T) {
		t.Setenv("TEST_INT_KEY", "42")
		got := envInt("TEST_INT_KEY", 0)
		if got != 42 {
			t.Errorf("expected 42, got %d", got)
		}
	})
	t.Run("zero", func(t *testing.T) {
		t.Setenv("TEST_INT_KEY2", "0")
		got := envInt("TEST_INT_KEY2", 10)
		if got != 0 {
			t.Errorf("expected 0, got %d", got)
		}
	})
	t.Run("not set returns fallback", func(t *testing.T) {
		got := envInt("TEST_INT_MISSING_XYZ", 99)
		if got != 99 {
			t.Errorf("expected fallback 99, got %d", got)
		}
	})
	t.Run("invalid value returns fallback", func(t *testing.T) {
		t.Setenv("TEST_INT_KEY3", "notanumber")
		got := envInt("TEST_INT_KEY3", 7)
		if got != 7 {
			t.Errorf("expected fallback 7 for invalid, got %d", got)
		}
	})
}
