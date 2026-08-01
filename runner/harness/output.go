package harness

import (
	"bufio"
	"io"
	"log/slog"
	"strings"
	"unicode/utf8"
)

const (
	maxOutputLineBytes = 4096
	maxOutputScanBytes = 64 * 1024 * 1024
)

// LogOutput writes line-oriented harness process output to the structured log.
// Individual lines are bounded so a noisy subprocess cannot create enormous
// log records, while the scanner accepts large lines before truncating them.
func LogOutput(harnessName, taskID, stream string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxOutputScanBytes)
	for scanner.Scan() {
		slog.Info(harnessName+" output",
			"taskID", taskID,
			"stream", stream,
			"line", truncateOutputLine(scanner.Text()),
		)
	}
	if err := scanner.Err(); err != nil {
		slog.Warn(harnessName+" read error", "taskID", taskID, "stream", stream, "err", err)
	}
}

func truncateOutputLine(line string) string {
	line = strings.ToValidUTF8(line, "�")
	if len(line) <= maxOutputLineBytes {
		return line
	}
	end := maxOutputLineBytes
	for end > 0 && !utf8.ValidString(line[:end]) {
		end--
	}
	return line[:end] + "... (truncated)"
}
