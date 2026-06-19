package pi

import (
	"bufio"
	"io"
	"log/slog"
)

func pipeOutput(taskID, stream string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for scanner.Scan() {
		slog.Info("pi output", "taskID", taskID, "stream", stream, "line", scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("pi output scanner error", "taskID", taskID, "stream", stream, "err", err)
	}
}
