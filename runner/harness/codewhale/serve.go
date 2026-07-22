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
	"strconv"
	"strings"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

const (
	serveReadyTimeout       = 15 * time.Second
	servePollInterval       = 500 * time.Millisecond
	serveHTTPTimeout        = 2 * time.Second
	eventReconnectAttempts  = 4
	eventReconnectBaseDelay = 250 * time.Millisecond
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

func createSession(ctx context.Context, baseURL, secret string) (string, error) {
	_, modelID := codewhaleModelFields(task.TaskRequest{})
	payload, _ := json.Marshal(map[string]any{
		"model":     modelID,
		"workspace": "/workspace",
		"mode":      "agent",
		"archived":  false,
	})
	resp, err := doPost(ctx, baseURL+"/v1/threads", secret, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("POST /v1/threads: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST /v1/threads: status %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("POST /v1/threads decode: %w", err)
	}
	if result.ID == "" {
		return "", fmt.Errorf("POST /v1/threads: response missing id")
	}
	return result.ID, nil
}

func (cw *CodeWhale) sendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error) {
	promptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	publishFn, tokenFn, err := cw.waitForCallbacks(promptCtx)
	if err != nil {
		return "", fmt.Errorf("wait for CodeWhale event callbacks: %w", err)
	}

	prompt := req.Prompt
	if len(req.Skills) > 0 {
		prompt = fmt.Sprintf("You have access to the following skills: %s. Use them when relevant.\n\n%s",
			strings.Join(req.Skills, ", "), prompt)
	}

	payload := map[string]any{
		"prompt": prompt,
	}
	_, modelID := codewhaleModelFields(req)
	if modelID != "" {
		payload["model"] = modelID
	}

	body, _ := json.Marshal(payload)
	url := baseURL + "/v1/threads/" + sessionID + "/turns"
	httpClient := &http.Client{Timeout: timeout}
	httpReq, err := http.NewRequestWithContext(promptCtx, "POST", url, bytes.NewReader(body))
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

	if resp.StatusCode != 200 && resp.StatusCode != 201 {
		return "", fmt.Errorf("POST /turns: status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("parse turn response: %w", err)
	}
	if result.Turn.ID == "" {
		return "", fmt.Errorf("POST /turns: response missing turn.id")
	}
	cw.setTurnID(sessionID, result.Turn.ID)

	summary, err := waitForTurnCompletion(promptCtx, baseURL, sessionID, result.Turn.ID, secret, publishFn, tokenFn)
	cw.setSessionExport(sessionID, renderMarkdownExport(sessionID, result.Turn.ID, prompt, summary, err))
	return summary, err
}

func (cw *CodeWhale) abortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	turnID := cw.turnID(sessionID)
	if turnID == "" {
		return nil
	}
	abortCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	path := "/v1/threads/" + sessionID + "/turns/" + turnID + "/interrupt"
	resp, err := doPost(abortCtx, baseURL+path, secret, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 && resp.StatusCode != 202 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		return fmt.Errorf("POST %s: status %d: %s", path, resp.StatusCode, string(body))
	}
	slog.Info("codewhale turn interrupted", "sessionID", sessionID, "turnID", turnID)
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

func watchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
	// The thread-scoped event stream is opened by SendPrompt for terminal waiting.
	// The generic watcher cannot know the thread ID until CreateSession has run,
	// so CodeWhale progress is emitted from the terminal wait path instead.
	<-ctx.Done()
}

func waitForTurnCompletion(ctx context.Context, baseURL, sessionID, turnID, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) (string, error) {
	var summary strings.Builder
	var pending []string
	var lastSeq uint64
	lastFlush := time.Now()

	flush := func(force bool) {
		if !force && time.Since(lastFlush) < 3*time.Second {
			return
		}
		if summary.Len() > 0 && publishFn != nil {
			publishFn("running", "codewhale: "+summary.String())
		}
		for _, s := range pending {
			if publishFn != nil {
				publishFn("running", s)
			}
		}
		pending = pending[:0]
		lastFlush = time.Now()
	}

	var streamErr error
	for attempt := 0; attempt <= eventReconnectAttempts; attempt++ {
		if attempt > 0 {
			delay := eventReconnectBaseDelay * time.Duration(1<<(attempt-1))
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return summary.String(), ctx.Err()
			case <-timer.C:
			}
		}

		eventsURL := baseURL + "/v1/threads/" + sessionID + "/events?since_seq=" + strconv.FormatUint(lastSeq, 10)
		req, err := http.NewRequestWithContext(ctx, "GET", eventsURL, nil)
		if err != nil {
			return summary.String(), fmt.Errorf("create event request: %w", err)
		}
		if secret != "" {
			req.Header.Set("Authorization", bearerAuthHeader(secret))
		}
		req.Header.Set("Accept", "text/event-stream")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return summary.String(), ctx.Err()
			}
			streamErr = err
			continue
		}
		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
			resp.Body.Close()
			return summary.String(), fmt.Errorf("GET /events: status %d: %s", resp.StatusCode, string(body))
		}

		br := newSSEReader(resp.Body)
		for {
			ev, err := br.Read()
			if err != nil {
				resp.Body.Close()
				if ctx.Err() != nil {
					return summary.String(), ctx.Err()
				}
				streamErr = err
				break
			}
			if ev == nil {
				continue
			}
			envelope, ok := decodeCodewhaleEnvelope(ev)
			if !ok {
				continue
			}
			if envelope.Seq > 0 {
				if envelope.Seq <= lastSeq {
					continue
				}
				lastSeq = envelope.Seq
			}
			if envelope.TurnID != turnID {
				continue
			}
			if ev.Type == "turn.completed" {
				resp.Body.Close()
				flush(true)
				if tokenFn != nil {
					usage := extractCodewhaleTokenUsage(envelope)
					tokenFn(*usage)
				}
				status := strings.ToLower(envelope.Payload.Turn.Status)
				switch status {
				case "completed":
					return summary.String(), nil
				case "failed", "interrupted", "canceled", "cancelled":
					if envelope.Payload.Turn.Error != "" {
						return summary.String(), fmt.Errorf("turn %s: %s", status, envelope.Payload.Turn.Error)
					}
					return summary.String(), fmt.Errorf("turn %s", status)
				default:
					return summary.String(), fmt.Errorf("turn completed with unknown status %q", envelope.Payload.Turn.Status)
				}
			}
			detail := summarizeCodewhaleEnvelope(ev.Type, envelope)
			if detail == "" {
				continue
			}
			if ev.Type == "item.delta" && envelope.Payload.Kind == "agent_message" {
				summary.WriteString(detail)
			} else {
				switch ev.Type {
				case "approval.required", "item.failed":
					flush(true)
					if publishFn != nil {
						publishFn("running", "codewhale: "+detail)
					}
				default:
					pending = append(pending, "codewhale: "+detail)
				}
			}
			flush(false)
		}
	}
	return summary.String(), fmt.Errorf("event stream ended before turn %s completed after %d reconnect attempts: %w", turnID, eventReconnectAttempts, streamErr)
}

type codewhaleEnvelope struct {
	Seq     uint64 `json:"seq"`
	Event   string `json:"event"`
	Kind    string `json:"kind"`
	TurnID  string `json:"turn_id"`
	Payload struct {
		Kind  string `json:"kind"`
		Delta string `json:"delta"`
		Name  string `json:"name"`
		Turn  struct {
			Status string `json:"status"`
			Error  string `json:"error"`
			Usage  struct {
				InputTokens          int64 `json:"input_tokens"`
				OutputTokens         int64 `json:"output_tokens"`
				PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
				PromptCacheTokens    int64 `json:"prompt_cache_tokens"`
				CachedTokens         int64 `json:"cached_tokens"`
				ReasoningTokens      int64 `json:"reasoning_tokens"`
			} `json:"usage"`
			CostUSD float64 `json:"cost_usd"`
		} `json:"turn"`
	} `json:"payload"`
}

func decodeCodewhaleEnvelope(ev *sseEvent) (codewhaleEnvelope, bool) {
	var envelope codewhaleEnvelope
	if err := json.Unmarshal([]byte(ev.Data), &envelope); err != nil {
		return envelope, false
	}
	if envelope.Event == "" {
		envelope.Event = ev.Type
	}
	if envelope.Kind == "" {
		envelope.Kind = envelope.Event
	}
	return envelope, true
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

func summarizeCodewhaleEnvelope(event string, envelope codewhaleEnvelope) string {
	switch event {
	case "turn.started":
		return "turn started"
	case "turn.lifecycle":
		if envelope.Payload.Turn.Status != "" {
			return "turn " + envelope.Payload.Turn.Status
		}
		return "turn update"
	case "item.started":
		if envelope.Payload.Kind != "" {
			return envelope.Payload.Kind + " started"
		}
		return "item started"
	case "item.delta":
		if envelope.Payload.Delta != "" {
			return envelope.Payload.Delta
		}
		return envelope.Payload.Kind
	case "item.completed":
		if envelope.Payload.Kind != "" {
			return envelope.Payload.Kind + " completed"
		}
		return "item completed"
	case "item.failed":
		return "item failed"
	case "approval.required":
		return "approval required"
	case "turn.completed":
		return ""
	default:
		return ""
	}
}

func extractCodewhaleTokenUsage(envelope codewhaleEnvelope) *task.TokenUsage {
	usage := envelope.Payload.Turn.Usage
	tu := &task.TokenUsage{
		InputTokens:     usage.InputTokens,
		OutputTokens:    usage.OutputTokens,
		CacheReadTokens: usage.PromptCacheHitTokens + usage.PromptCacheTokens + usage.CachedTokens,
		ReasoningTokens: usage.ReasoningTokens,
	}
	if envelope.Payload.Turn.CostUSD != 0 {
		tu.CostCents = int64(envelope.Payload.Turn.CostUSD * 100)
	}
	return tu
}

func renderMarkdownExport(sessionID, turnID, prompt, summary string, err error) string {
	var sb strings.Builder
	sb.WriteString("# CodeWhale Session\n\n")
	sb.WriteString("- Thread: `")
	sb.WriteString(sessionID)
	sb.WriteString("`\n")
	sb.WriteString("- Turn: `")
	sb.WriteString(turnID)
	sb.WriteString("`\n")
	if err != nil {
		sb.WriteString("- Status: failed\n\n")
	} else {
		sb.WriteString("- Status: completed\n\n")
	}
	sb.WriteString("## User\n\n")
	sb.WriteString(prompt)
	sb.WriteString("\n\n")
	if summary != "" {
		sb.WriteString("## Assistant\n\n")
		sb.WriteString(summary)
		sb.WriteString("\n\n")
	}
	if err != nil {
		sb.WriteString("## Error\n\n")
		sb.WriteString(err.Error())
		sb.WriteString("\n")
	}
	return sb.String()
}

func codewhaleServeCommand(port int) []string {
	return []string{"codewhale", "app-server", "--http", "--host", "0.0.0.0", "--port", strconv.Itoa(port)}
}

func codewhaleServeArgsResume(port int) []string {
	return codewhaleServeCommand(port)[1:]
}

func codewhaleModelFields(req task.TaskRequest) (provider, model string) {
	provider = req.ProviderID
	model = req.ModelID
	if model == "" {
		model = "deepseek-v4-flash-free"
	}
	if provider == "" {
		provider = "deepseek"
	}
	return
}
