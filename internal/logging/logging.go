// Package logging configures the server's structured slog output (issue #87).
// Operators pick a minimum level (debug/info/warn/error) and a text or JSON
// format. Invalid configuration is rejected with a clear error so startup
// fails instead of silently mis-logging.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/flatout-works/chetter/internal/config"
)

// Handler builds a slog.Handler from cfg writing to w. Text mode stays
// human-readable; JSON mode emits one valid JSON object per record. An
// unset CHETTER_LOG_FORMAT defaults to text and an unset level to info.
func Handler(cfg config.Logging, w io.Writer) (slog.Handler, error) {
	level := cfg.Level
	if raw := strings.TrimSpace(cfg.LevelRaw); raw != "" {
		if err := level.UnmarshalText([]byte(raw)); err != nil {
			return nil, fmt.Errorf("CHETTER_LOG_LEVEL %q is invalid (want debug, info, warn, or error): %w", raw, err)
		}
	}
	opts := &slog.HandlerOptions{Level: level}
	switch cfg.Format {
	case "", "text":
		return slog.NewTextHandler(w, opts), nil
	case "json":
		return slog.NewJSONHandler(w, opts), nil
	default:
		return nil, fmt.Errorf("CHETTER_LOG_FORMAT %q is invalid (want text or json)", cfg.Format)
	}
}

// Setup configures the process-wide slog default logger from cfg and returns
// it. Call once at startup; invalid configuration returns a clear error
// (issue #87).
func Setup(cfg config.Logging) (*slog.Logger, error) {
	handler, err := Handler(cfg, os.Stderr)
	if err != nil {
		return nil, err
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)
	return logger, nil
}
