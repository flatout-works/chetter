package opencode

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
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

func watchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage), sessionID string, onIdle func()) {
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

	var textBuf strings.Builder
	var pending []string
	lastFlush := time.Now()

	flush := func(force bool) {
		if !force && time.Since(lastFlush) < 3*time.Second {
			return
		}
		if textBuf.Len() > 0 {
			publishFn("running", "opencode: "+truncate(textBuf.String()))
			textBuf.Reset()
		}
		for _, s := range pending {
			publishFn("running", s)
		}
		pending = pending[:0]
		lastFlush = time.Now()
	}

	for {
		line, readErr := br.ReadString('\n')
		if readErr != nil {
			if readErr != io.EOF && ctx.Err() == nil {
				slog.Warn("opencode event stream read failed", "taskID", taskID, "err", readErr)
			}
			flush(true)
			return
		}
		line = strings.TrimRight(line, "\n\r")
		if strings.TrimSpace(line) == "" {
			if len(dataLines) > 0 {
				raw := strings.Join(dataLines, "\n")
				typeName, props := parseEventType(raw)
				slog.Info("opencode event", "taskID", taskID, "type", typeName)
				if tokenFn != nil {
					if usage := extractTokenUsage(dataLines); usage != nil {
						tokenFn(*usage)
					}
				}
				switch typeName {
				case "message.part.delta":
					if text := extractOpenCodeDeltaText(props); text != "" {
						textBuf.WriteString(text)
						flush(false)
					}
				case "session.status":
					if onIdle != nil && isSessionIdleStatus(props, sessionID) {
						slog.Info("session.status idle event received", "taskID", taskID, "sessionID", sessionID)
						onIdle()
					}
					detail := summarizeEvent(raw)
					if detail != "" {
						flush(true)
						publishFn("running", "opencode: "+detail)
					}
				case "session.idle":
					if onIdle != nil && isSessionIdleEvent(props, sessionID) {
						slog.Info("session.idle event received", "taskID", taskID, "sessionID", sessionID)
						onIdle()
					}
					detail := summarizeEvent(raw)
					if detail != "" {
						flush(true)
						publishFn("running", "opencode: "+detail)
					}
				case "message.updated":
					detail := summarizeEvent(raw)
					if detail != "" {
						flush(true)
						publishFn("running", "opencode: "+detail)
					}
				case "error", "session.error":
					detail := summarizeEvent(raw)
					if detail != "" {
						flush(true)
						publishFn("running", "opencode: "+detail)
					}
				default:
					detail := summarizeEvent(raw)
					if detail != "" {
						pending = append(pending, "opencode: "+detail)
						flush(false)
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

func parseEventType(raw string) (string, map[string]any) {
	var evt map[string]any
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		return "", nil
	}
	typeName, _ := evt["type"].(string)
	props, _ := evt["properties"].(map[string]any)
	if props == nil {
		props, _ = evt["data"].(map[string]any)
	}
	return typeName, props
}

func extractOpenCodeDeltaText(props map[string]any) string {
	if props == nil {
		return ""
	}
	delta, _ := props["delta"].(map[string]any)
	if delta == nil {
		return ""
	}
	text, _ := delta["text"].(string)
	return text
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

func truncate(s string) string {
	const maxSummaryBytes = 8000
	if len(s) > maxSummaryBytes {
		return s[:maxSummaryBytes] + "\n... (truncated)"
	}
	return s
}

func extractTokenUsage(dataLines []string) *task.TokenUsage {
	for _, line := range dataLines {
		var evt map[string]any
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		typeName, _ := evt["type"].(string)
		if typeName != "message.part.updated" {
			continue
		}
		props, _ := evt["properties"].(map[string]any)
		if props == nil {
			continue
		}
		part, _ := props["part"].(map[string]any)
		if part == nil {
			continue
		}
		partType, _ := part["type"].(string)
		if partType != "step-finish" {
			continue
		}
		tokens, _ := part["tokens"].(map[string]any)
		if tokens == nil {
			continue
		}
		cache, _ := tokens["cache"].(map[string]any)
		cost, _ := part["cost"].(float64)
		usage := &task.TokenUsage{
			InputTokens:     floatToInt64(tokens["input"]),
			OutputTokens:    floatToInt64(tokens["output"]),
			ReasoningTokens: floatToInt64(tokens["reasoning"]),
			CostCents:       int64(math.Round(cost * 100)),
		}
		if cache != nil {
			usage.CacheReadTokens = floatToInt64(cache["read"])
			usage.CacheWriteTokens = floatToInt64(cache["write"])
		}
		return usage
	}
	return nil
}

func floatToInt64(v any) int64 {
	switch val := v.(type) {
	case float64:
		return int64(val)
	case float32:
		return int64(val)
	case int64:
		return val
	case int:
		return int64(val)
	default:
		return 0
	}
}

func isSessionIdleEvent(props map[string]any, sessionID string) bool {
	return eventBelongsToSession(props, sessionID)
}

func eventBelongsToSession(props map[string]any, sessionID string) bool {
	if props == nil {
		return false
	}
	id, _ := props["sessionID"].(string)
	if id == "" {
		id, _ = props["id"].(string)
	}
	return id != "" && id == sessionID
}

// isSessionIdleStatus checks whether a session.status SSE event indicates the
// session has transitioned to an idle/complete state. It handles several
// possible property layouts defensively.
func isSessionIdleStatus(props map[string]any, sessionID string) bool {
	if !eventBelongsToSession(props, sessionID) {
		return false
	}
	statusType, _ := props["type"].(string)
	if statusType == "" {
		if status, ok := props["status"].(map[string]any); ok {
			statusType, _ = status["type"].(string)
		}
	}
	if statusType == "" {
		statusType, _ = props["status"].(string)
	}
	switch statusType {
	case "idle", "completed", "finished", "done":
		return true
	}
	return false
}
