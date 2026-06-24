package claude

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

const (
	serveReadyTimeout = 15 * time.Second
	servePollInterval = 500 * time.Millisecond
	serveHTTPTimeout  = 2 * time.Second
)

func generatePassword() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func basicAuthHeader(secret string) string {
	auth := base64.StdEncoding.EncodeToString([]byte("opencode:" + secret))
	return "Basic " + auth
}

func doPost(ctx context.Context, url, secret string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	return http.DefaultClient.Do(req)
}

func waitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: serveHTTPTimeout}
	var lastErr error
	var lastStatus int
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/config", nil)
		if err != nil {
			lastErr = err
			time.Sleep(servePollInterval)
			continue
		}
		if secret != "" {
			req.Header.Set("Authorization", basicAuthHeader(secret))
		}
		resp, err := client.Do(req)
		if err == nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				resp.Body.Close()
				return nil
			}
			lastStatus = resp.StatusCode
			resp.Body.Close()
		} else {
			lastErr = err
		}
		time.Sleep(servePollInterval)
	}
	if lastStatus >= 400 {
		return fmt.Errorf("server at %s not responding within %v: last status: %d", baseURL, timeout, lastStatus)
	}
	if lastErr != nil {
		return fmt.Errorf("server at %s not responding within %v: last error: %w", baseURL, timeout, lastErr)
	}
	return fmt.Errorf("server at %s not responding within %v", baseURL, timeout)
}

func createSession(ctx context.Context, baseURL, secret string) (string, error) {
	resp, err := doPost(ctx, baseURL+"/session", secret, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return "", fmt.Errorf("POST /session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST /session: status %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("POST /session decode: %w", err)
	}
	return result.SessionID, nil
}

func sendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error) {
	_, model := claudeModelFields(req)

	payload := map[string]any{
		"prompt": promptWithSkillHints(req.Prompt, req.Skills),
		"model":  model,
		"agent":  req.Agent,
	}
	if req.ResumeHarnessSessionID != "" {
		payload["resume_session_id"] = req.ResumeHarnessSessionID
	}
	if len(req.Skills) > 0 {
		payload["skills"] = req.Skills
	}

	body, _ := json.Marshal(payload)
	url := baseURL + "/session/" + sessionID + "/message"
	httpClient := &http.Client{Timeout: timeout}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if secret != "" {
		httpReq.Header.Set("Authorization", basicAuthHeader(secret))
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("POST /message: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	slog.Info("claude message response", "status", resp.StatusCode, "len", len(respBody))

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("POST /message: status %d: %s", resp.StatusCode, string(respBody))
	}

	return "", nil
}

func abortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	abortCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := doPost(abortCtx, baseURL+"/session/"+sessionID+"/abort", secret, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return fmt.Errorf("POST /session/%s/abort: %w", sessionID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		return fmt.Errorf("POST /session/%s/abort: status %d: %s", sessionID, resp.StatusCode, string(body))
	}
	slog.Info("claude session aborted", "sessionID", sessionID)
	return nil
}

func exportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	exportCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(exportCtx, "GET", baseURL+"/session/"+sessionID+"/export", nil)
	if err != nil {
		return "", fmt.Errorf("create export request: %w", err)
	}
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET /export: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		return "", fmt.Errorf("GET /export: status %d: %s", resp.StatusCode, string(body))
	}

	exportBody, _ := io.ReadAll(resp.Body)
	return string(exportBody), nil
}

func watchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/event", nil)
	if err != nil {
		slog.Warn("claude event request failed", "taskID", taskID, "err", err)
		return
	}
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("claude event stream failed", "taskID", taskID, "err", err)
		}
		return
	}
	defer resp.Body.Close()

	br := newSSEReader(resp.Body)
	var lastPublished time.Time
	for {
		ev, err := br.Read()
		if err != nil {
			if ctx.Err() == nil && err != io.EOF {
				slog.Warn("claude event read failed", "taskID", taskID, "err", err)
			}
			return
		}
		if ev == nil {
			continue
		}
		detail := summarizeClaudeEvent(ev)
		if detail != "" {
			if time.Since(lastPublished) >= 3*time.Second || strings.Contains(detail, "error") {
				publishFn("running", "claude: "+detail)
				lastPublished = time.Now()
			}
		}
		if ev.Type == "result" && tokenFn != nil {
			if usage := extractClaudeTokenUsage(ev.Data); usage != nil {
				tokenFn(*usage)
			}
		}
	}
}

type sseEvent struct {
	Type string
	Data string
}

type sseReader struct {
	br *bufio.Reader
}

func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{
		br: bufio.NewReader(r),
	}
}

func (r *sseReader) Read() (*sseEvent, error) {
	var ev sseEvent
	for {
		line, err := r.br.ReadString('\n')
		if err != nil {
			if ev.Type != "" || ev.Data != "" {
				result := &sseEvent{Type: ev.Type, Data: ev.Data}
				return result, nil
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if ev.Type != "" || ev.Data != "" {
				result := &sseEvent{Type: ev.Type, Data: ev.Data}
				return result, nil
			}
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			ev.Type = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			if ev.Data == "" {
				ev.Data = strings.TrimPrefix(line, "data: ")
			} else {
				ev.Data += "\n" + strings.TrimPrefix(line, "data: ")
			}
		}
	}
}

func summarizeClaudeEvent(ev *sseEvent) string {
	switch ev.Type {
	case "text_delta":
		var data map[string]any
		if json.Unmarshal([]byte(ev.Data), &data) == nil {
			if delta, ok := data["delta"].(map[string]any); ok {
				if text, ok := delta["text"].(string); ok && text != "" {
					return text
				}
			}
		}
		return "text"
	case "tool_use":
		var data map[string]any
		if json.Unmarshal([]byte(ev.Data), &data) == nil {
			if block, ok := data["content_block"].(map[string]any); ok {
				if name, ok := block["name"].(string); ok {
					return "tool_use: " + name
				}
			}
		}
		return "tool_use"
	case "system_init":
		return "system.init"
	case "api_retry":
		return "system.api_retry"
	case "done":
		return ""
	case "error":
		return ev.Data
	default:
		return ""
	}
}

func extractClaudeTokenUsage(data string) *task.TokenUsage {
	var ev map[string]any
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil
	}
	usage, _ := ev["usage"].(map[string]any)
	if usage == nil {
		return nil
	}
	tu := &task.TokenUsage{
		InputTokens:  floatToInt64(usage["input_tokens"]),
		OutputTokens: floatToInt64(usage["output_tokens"]),
	}
	if cost, ok := ev["total_cost_usd"].(float64); ok {
		tu.CostCents = int64(cost * 100)
	}
	return tu
}

func floatToInt64(v any) int64 {
	switch val := v.(type) {
	case float64:
		return int64(val)
	case int64:
		return val
	default:
		return 0
	}
}

func promptWithSkillHints(prompt string, skills []string) string {
	if len(skills) == 0 {
		return prompt
	}
	return fmt.Sprintf("You have access to the following skills: %s. Use them when relevant.\n\n%s",
		strings.Join(skills, ", "), prompt)
}

func claudeServeCommand(port int) []string {
	return []string{"claude-serve-proxy", "--port", strconv.Itoa(port)}
}

func claudeServeArgsResume(port int) []string {
	return claudeServeCommand(port)[1:]
}
