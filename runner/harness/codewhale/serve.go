package codewhale

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
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

func bearerAuthHeader(secret string) string {
	return "Bearer " + secret
}

func doPost(ctx context.Context, url, secret string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", bearerAuthHeader(secret))
	}
	return http.DefaultClient.Do(req)
}

func doGet(ctx context.Context, url, secret string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if secret != "" {
		req.Header.Set("Authorization", bearerAuthHeader(secret))
	}
	return http.DefaultClient.Do(req)
}

// waitForReady polls GET /health until the server responds 2xx.
// TODO: confirm exact readiness endpoint (might be /config or /health).
func waitForReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: serveHTTPTimeout}
	var lastErr error
	var lastStatus int
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/health", nil)
		if err != nil {
			lastErr = err
			time.Sleep(servePollInterval)
			continue
		}
		if secret != "" {
			req.Header.Set("Authorization", bearerAuthHeader(secret))
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
		return fmt.Errorf("codewhale server at %s not responding within %v: last status: %d", baseURL, timeout, lastStatus)
	}
	if lastErr != nil {
		return fmt.Errorf("codewhale server at %s not responding within %v: last error: %w", baseURL, timeout, lastErr)
	}
	return fmt.Errorf("codewhale server at %s not responding within %v", baseURL, timeout)
}

// createSession sends POST /v1/thread to create a new thread.
// TODO: confirm exact endpoint path and request/response JSON shape.
func createSession(ctx context.Context, baseURL, secret string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"cwd": "/workspace",
	})
	resp, err := doPost(ctx, baseURL+"/v1/thread", secret, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("POST /v1/thread: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST /v1/thread: status %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		ThreadID string `json:"thread_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("POST /v1/thread decode: %w", err)
	}
	return result.ThreadID, nil
}

// sendPrompt sends POST /v1/thread/{id}/prompt to submit a user message.
// TODO: confirm exact endpoint path, request shape, and response fields.
func sendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error) {
	providerID, modelID := codewhaleModelFields(req)

	prompt := req.Prompt
	if len(req.Skills) > 0 {
		prompt = fmt.Sprintf("You have access to the following skills: %s. Use them when relevant.\n\n%s",
			strings.Join(req.Skills, ", "), prompt)
	}

	payload := map[string]any{
		"prompt":         prompt,
		"model":          modelID,
		"model_provider": providerID,
	}
	if req.ResumeHarnessSessionID != "" {
		payload["resume_thread_id"] = req.ResumeHarnessSessionID
	}

	body, _ := json.Marshal(payload)
	url := baseURL + "/v1/thread/" + sessionID + "/prompt"
	httpClient := &http.Client{Timeout: timeout}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if secret != "" {
		httpReq.Header.Set("Authorization", bearerAuthHeader(secret))
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("POST /prompt: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	slog.Info("codewhale prompt response", "status", resp.StatusCode, "len", len(respBody))

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("POST /prompt: status %d: %s", resp.StatusCode, string(respBody))
	}

	return "", nil
}

// abortSession sends POST /v1/thread/{id}/abort to cancel a running thread.
// TODO: confirm exact endpoint path.
func abortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	abortCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := doPost(abortCtx, baseURL+"/v1/thread/"+sessionID+"/abort", secret, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return fmt.Errorf("POST /v1/thread/%s/abort: %w", sessionID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		return fmt.Errorf("POST /v1/thread/%s/abort: status %d: %s", sessionID, resp.StatusCode, string(body))
	}
	slog.Info("codewhale session aborted", "sessionID", sessionID)
	return nil
}

// exportSession retrieves the session transcript via the HTTP API.
// TODO: confirm exact endpoint path (GET /v1/thread/{id}/export or similar).
func exportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	exportCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := doGet(exportCtx, baseURL+"/v1/thread/"+sessionID+"/export", secret)
	if err != nil {
		return "", fmt.Errorf("GET /v1/thread/%s/export: %w", sessionID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		return "", fmt.Errorf("GET /v1/thread/%s/export: status %d: %s", sessionID, resp.StatusCode, string(body))
	}
	exportBody, _ := io.ReadAll(resp.Body)
	return string(exportBody), nil
}

// watchEvents connects to the SSE event stream and publishes status updates.
// TODO: confirm exact SSE endpoint path and event type names.
func watchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/v1/event", nil)
	if err != nil {
		slog.Warn("codewhale event request failed", "taskID", taskID, "err", err)
		return
	}
	if secret != "" {
		req.Header.Set("Authorization", bearerAuthHeader(secret))
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("codewhale event stream failed", "taskID", taskID, "err", err)
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
				slog.Warn("codewhale event read failed", "taskID", taskID, "err", err)
			}
			return
		}
		if ev == nil {
			continue
		}
		detail := summarizeCodewhaleEvent(ev)
		if detail != "" {
			if time.Since(lastPublished) >= 3*time.Second || strings.Contains(detail, "error") {
				publishFn("running", "codewhale: "+detail)
				lastPublished = time.Now()
			}
		}
		if ev.Type == "result" && tokenFn != nil {
			if usage := extractCodewhaleTokenUsage(ev.Data); usage != nil {
				tokenFn(*usage)
			}
		}
	}
}

// SSE parsing (modeled on claude/serve.go SSE reader).
type sseEvent struct {
	Type string
	Data string
}

type sseReader struct {
	br *bufio.Reader
}

func newSSEReader(r io.Reader) *sseReader {
	return &sseReader{br: bufio.NewReader(r)}
}

func (r *sseReader) Read() (*sseEvent, error) {
	var ev sseEvent
	for {
		line, err := r.br.ReadString('\n')
		if err != nil {
			if ev.Type != "" || ev.Data != "" {
				return &ev, nil
			}
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if ev.Type != "" || ev.Data != "" {
				return &ev, nil
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

// summarizeCodewhaleEvent extracts a human-readable summary from an SSE event.
// TODO: confirm actual event type names from the CodeWhale serve API.
func summarizeCodewhaleEvent(ev *sseEvent) string {
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
	case "reasoning_delta":
		return "reasoning"
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
	case "tool_result":
		return "tool_result"
	case "system_init":
		return "system.init"
	case "done":
		return ""
	case "error":
		return ev.Data
	default:
		return ""
	}
}

// extractCodewhaleTokenUsage extracts token usage from a result event.
// TODO: confirm exact token usage field names from the CodeWhale API.
func extractCodewhaleTokenUsage(data string) *task.TokenUsage {
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
	if reasoning, ok := usage["reasoning_tokens"]; ok {
		tu.ReasoningTokens = floatToInt64(reasoning)
	}
	if cacheRead, ok := usage["cache_read_tokens"]; ok {
		tu.CacheReadTokens = floatToInt64(cacheRead)
	}
	if cacheWrite, ok := usage["cache_write_tokens"]; ok {
		tu.CacheWriteTokens = floatToInt64(cacheWrite)
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

func codewhaleServeCommand(port int) []string {
	return []string{"codewhale", "serve", "--port", strconv.Itoa(port)}
}

func codewhaleServeArgsResume(port int) []string {
	return codewhaleServeCommand(port)[1:]
}

func codewhaleModelFields(req task.TaskRequest) (provider, model string) {
	provider = req.ProviderID
	model = req.ModelID
	if model == "" {
		model = os.Getenv("CODEWHALE_MODEL")
	}
	if model == "" {
		model = "deepseek-v4-flash-free"
	}
	if provider == "" {
		provider = "deepseek"
	}
	return
}
