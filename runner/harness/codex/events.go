package codex

import (
	"bufio"
	"io"
	"log/slog"
)

func pipeOutput(taskID, stream string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if len(line) > 4096 {
			line = line[:4096] + "... (truncated)"
		}
		slog.Info("codex output", "taskID", taskID, "stream", stream, "line", line)
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("codex read error", "taskID", taskID, "stream", stream, "err", err)
	}
}
