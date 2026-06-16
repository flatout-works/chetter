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

func pipeOutput(taskID, stream string, r io.Reader) {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				slog.Warn("opencode output read failed", "taskID", taskID, "stream", stream, "err", err)
			}
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		slog.Info("opencode output", "taskID", taskID, "stream", stream, "line", truncate(line))
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

	br := bufio.NewReader(resp.Body)
	var dataLines []string
	lastPublished := time.Time{}
	for {
		line, readErr := br.ReadString('\n')
		if readErr != nil {
			if readErr != io.EOF && ctx.Err() == nil {
				slog.Warn("opencode event stream read failed", "taskID", taskID, "err", readErr)
			}
			return
		}
		line = strings.TrimRight(line, "\n\r")
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
