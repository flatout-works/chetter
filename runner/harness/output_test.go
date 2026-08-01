package harness

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestLogOutputIncludesFinalUnterminatedLine(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	LogOutput("pi", "task-1", "stdout", strings.NewReader("first line\nfinal line"))

	got := logs.String()
	for _, want := range []string{"msg=\"pi output\"", "taskID=task-1", "stream=stdout", "line=\"first line\"", "line=\"final line\""} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs do not contain %q:\n%s", want, got)
		}
	}
}

func TestTruncateOutputLine(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		truncated bool
	}{
		{name: "short", line: "hello"},
		{name: "long ASCII", line: strings.Repeat("a", maxOutputLineBytes+1), truncated: true},
		{name: "long Unicode", line: strings.Repeat("界", maxOutputLineBytes), truncated: true},
		{name: "invalid UTF-8", line: string([]byte{'a', 0xff, 'b'})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateOutputLine(tt.line)
			if !utf8.ValidString(got) {
				t.Fatalf("truncateOutputLine returned invalid UTF-8: %q", got)
			}
			if tt.truncated != strings.HasSuffix(got, "... (truncated)") {
				t.Fatalf("truncated suffix = %v, want %v", strings.HasSuffix(got, "... (truncated)"), tt.truncated)
			}
			if !tt.truncated && tt.name != "invalid UTF-8" && got != tt.line {
				t.Fatalf("truncateOutputLine() = %q, want %q", got, tt.line)
			}
		})
	}
}
