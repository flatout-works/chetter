package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/internal/config"
)

func TestHandlerFormats(t *testing.T) {
	t.Run("text mode stays human-readable", func(t *testing.T) {
		var buf bytes.Buffer
		h, err := Handler(config.Logging{Format: "text", Level: slog.LevelInfo}, &buf)
		if err != nil {
			t.Fatalf("Handler: %v", err)
		}
		slog.New(h).Info("hello", "request_id", "req_abc")
		out := buf.String()
		if !strings.Contains(out, "level=INFO") {
			t.Errorf("text output missing level=INFO: %q", out)
		}
		if !strings.Contains(out, "msg=hello") {
			t.Errorf("text output missing msg: %q", out)
		}
		if !strings.Contains(out, "request_id=req_abc") {
			t.Errorf("text output missing request_id: %q", out)
		}
	})

	t.Run("json mode emits valid structured JSON", func(t *testing.T) {
		var buf bytes.Buffer
		h, err := Handler(config.Logging{Format: "json", Level: slog.LevelInfo}, &buf)
		if err != nil {
			t.Fatalf("Handler: %v", err)
		}
		slog.New(h).Info("hello", "request_id", "req_abc")
		var rec map[string]any
		if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
			t.Fatalf("json output is not valid JSON: %v (output %q)", err, buf.String())
		}
		if rec["msg"] != "hello" {
			t.Errorf("expected msg hello, got %v", rec["msg"])
		}
		if rec["request_id"] != "req_abc" {
			t.Errorf("expected request_id req_abc, got %v", rec["request_id"])
		}
		if rec["level"] != "INFO" {
			t.Errorf("expected level INFO, got %v", rec["level"])
		}
	})
}

func TestHandlerLevelFiltering(t *testing.T) {
	log := func(cfg config.Logging) string {
		var buf bytes.Buffer
		h, err := Handler(cfg, &buf)
		if err != nil {
			t.Fatalf("Handler: %v", err)
		}
		logger := slog.New(h)
		logger.Debug("debug line")
		logger.Info("info line")
		logger.Warn("warn line")
		return buf.String()
	}

	t.Run("info level drops debug", func(t *testing.T) {
		out := log(config.Logging{Format: "text", Level: slog.LevelInfo})
		if strings.Contains(out, "debug line") {
			t.Errorf("debug line should be filtered at info level: %q", out)
		}
		if !strings.Contains(out, "info line") || !strings.Contains(out, "warn line") {
			t.Errorf("info/warn lines should be present: %q", out)
		}
	})

	t.Run("debug level keeps everything", func(t *testing.T) {
		out := log(config.Logging{Format: "text", Level: slog.LevelDebug})
		if !strings.Contains(out, "debug line") {
			t.Errorf("debug line should be present at debug level: %q", out)
		}
		if !strings.Contains(out, "info line") {
			t.Errorf("info line should be present: %q", out)
		}
	})

	t.Run("warn level drops info and debug", func(t *testing.T) {
		out := log(config.Logging{Format: "text", Level: slog.LevelWarn})
		if strings.Contains(out, "info line") || strings.Contains(out, "debug line") {
			t.Errorf("info/debug lines should be filtered at warn level: %q", out)
		}
		if !strings.Contains(out, "warn line") {
			t.Errorf("warn line should be present: %q", out)
		}
	})
}

func TestHandlerInvalidConfig(t *testing.T) {
	t.Run("invalid level", func(t *testing.T) {
		_, err := Handler(config.Logging{LevelRaw: "verbose", Format: "text"}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error for invalid level")
		}
		if !strings.Contains(err.Error(), "CHETTER_LOG_LEVEL") {
			t.Errorf("expected error to name CHETTER_LOG_LEVEL, got %q", err.Error())
		}
	})
	t.Run("invalid format", func(t *testing.T) {
		_, err := Handler(config.Logging{Format: "xml"}, &bytes.Buffer{})
		if err == nil {
			t.Fatal("expected error for invalid format")
		}
		if !strings.Contains(err.Error(), "CHETTER_LOG_FORMAT") {
			t.Errorf("expected error to name CHETTER_LOG_FORMAT, got %q", err.Error())
		}
	})
}
