package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const opencodeEventLineMax = 64 * 1024 * 1024

func pipeOutput(taskID, stream string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), opencodeEventLineMax)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		slog.Info("opencode output", "taskID", taskID, "stream", stream, "line", truncate(line))
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("opencode output read failed", "taskID", taskID, "stream", stream, "err", err)
	}
}

func watchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string)) {
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/event", nil)
	if err != nil {
		slog.Warn("opencode event request failed", "taskID", taskID, "err", err)
		return
	}
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("opencode event stream failed", "taskID", taskID, "err", err)
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		slog.Warn("opencode event stream returned non-200", "taskID", taskID, "status", resp.StatusCode, "body", string(body))
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), opencodeEventLineMax)
	var dataLines []string
	lastPublished := time.Time{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if len(dataLines) > 0 {
				detail := summarizeEvent(strings.Join(dataLines, "\n"))
				if detail != "" {
					slog.Info("opencode event", "taskID", taskID, "detail", detail)
					if time.Since(lastPublished) >= 3*time.Second || strings.Contains(detail, "error") || strings.Contains(detail, "permission") {
						publishFn("running", "opencode: "+detail)
						lastPublished = time.Now()
					}
				}
				dataLines = nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		slog.Warn("opencode event stream read failed", "taskID", taskID, "err", err)
	}
}

func summarizeEvent(raw string) string {
	var evt map[string]any
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		trimmed := strings.TrimSpace(raw)
		if len(trimmed) > 300 {
			trimmed = trimmed[:300] + "..."
		}
		return trimmed
	}
	typeName, _ := evt["type"].(string)
	if typeName == "" {
		return ""
	}
	props, _ := evt["properties"].(map[string]any)
	if props == nil {
		props, _ = evt["data"].(map[string]any)
	}
	switch typeName {
	case "session.status":
		return typeName + " " + compactJSON(props)
	case "session.error", "permission.asked", "permission.replied", "file.edited", "command.executed", "message.updated", "message.part.updated", "message.part.delta":
		return typeName + " " + compactJSON(props)
	default:
		return typeName
	}
}

func compactJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	text := string(data)
	if len(text) > 500 {
		return text[:500] + "..."
	}
	return text
}

func summarizeJSONL(out string) string {
	type event struct {
		Type string `json:"type"`
		Part struct {
			Text string `json:"text"`
		} `json:"part"`
	}

	var texts []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var evt event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		if evt.Type == "text" && strings.TrimSpace(evt.Part.Text) != "" {
			texts = append(texts, evt.Part.Text)
		}
	}
	if len(texts) > 0 {
		return strings.Join(texts, "\n")
	}
	return out
}

func truncate(s string) string {
	const maxSummaryBytes = 8000
	if len(s) > maxSummaryBytes {
		return s[:maxSummaryBytes] + "\n... (truncated)"
	}
	return s
}
