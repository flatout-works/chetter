package opencode

import (
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

	"github.com/flatout-works/chetter/runner/harness/transport"
	"github.com/flatout-works/chetter/runner/internal/task"
)

func basicAuthHeader(secret string) string {
	auth := base64.StdEncoding.EncodeToString([]byte("opencode:" + secret))
	return "Basic " + auth
}

func generatePassword() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func doPost(ctx context.Context, url, contentType, secret string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	return http.DefaultClient.Do(req)
}

func waitForServeReady(ctx context.Context, baseURL, secret string, timeout time.Duration) error {
	return transport.WaitForReady(ctx, baseURL, "/config", func(req *http.Request) {
		if secret != "" {
			req.Header.Set("Authorization", basicAuthHeader(secret))
		}
	}, timeout, "server")
}

func createOpenCodeSession(ctx context.Context, baseURL, secret string) (string, error) {
	resp, err := doPost(ctx, baseURL+"/session", "application/json", secret, strings.NewReader("{}"))
	if err != nil {
		return "", fmt.Errorf("POST /session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST /session: status %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("POST /session decode: %w", err)
	}
	return result.ID, nil
}

func sendPromptAndWait(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration, idleCh <-chan struct{}) (string, error) {
	payload := openCodePromptPayload(req, wsDir, promptWithSkillHints(req.Prompt, req.Skills))

	if err := startAsyncPrompt(ctx, baseURL, sessionID, secret, payload); err != nil {
		return "", err
	}

	if err := waitForSessionIdle(ctx, baseURL, sessionID, secret, timeout, idleCh); err != nil {
		return "", err
	}

	return fetchSessionSummary(ctx, baseURL, sessionID, secret)
}

func continueSession(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string) error {
	payload := openCodePromptPayload(req, wsDir, "Continue working on the current task now. Resume from the existing state and complete the requested work without waiting for more input.")
	return startAsyncPrompt(ctx, baseURL, sessionID, secret, payload)
}

func openCodePromptPayload(req task.TaskRequest, wsDir, prompt string) []byte {
	agentProvider, agentModel := agentModelFromConfig(wsDir, req.Agent)
	defaultProvider := agentProvider
	if defaultProvider == "" {
		defaultProvider = "opencode"
	}
	defaultModel := agentModel
	if defaultModel == "" {
		defaultModel = "deepseek-v4-flash-free"
	}
	providerID, modelID := promptModel(req, defaultProvider, defaultModel)
	variantID := promptVariant(req)
	slog.Info("sendPromptAndWait model", "provider", providerID, "model", modelID, "variant", variantID, "agent", req.Agent)
	model := map[string]any{
		"providerID": providerID,
		"modelID":    modelID,
	}
	if variantID != "" {
		model["variant"] = variantID
	}
	payload, _ := json.Marshal(map[string]any{
		"parts": []map[string]any{
			{"type": "text", "text": prompt},
		},
		"model": model,
	})
	return payload
}

func startAsyncPrompt(ctx context.Context, baseURL, sessionID, secret string, payload []byte) error {
	url := baseURL + "/session/" + sessionID + "/prompt_async"
	client := &http.Client{Timeout: 30 * time.Second}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("create prompt_async request: %w", err)
		}
		httpReq.Header.Set("Content-Type", "application/json")
		if secret != "" {
			httpReq.Header.Set("Authorization", basicAuthHeader(secret))
		}
		resp, err := client.Do(httpReq)
		if err != nil {
			lastErr = err
			slog.Warn("prompt_async POST failed, checking if session is already busy", "sessionID", sessionID, "attempt", attempt, "err", err)
			if status, sErr := getSessionStatus(ctx, baseURL, sessionID, secret); sErr == nil && status == "busy" {
				slog.Info("session already busy after POST failure, continuing to poll", "sessionID", sessionID)
				return nil
			}
			if attempt < 2 {
				time.Sleep(2 * time.Second)
			}
			continue
		}
		if resp.StatusCode == 204 || resp.StatusCode == 200 {
			resp.Body.Close()
			return nil
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		resp.Body.Close()
		lastErr = fmt.Errorf("POST /prompt_async: status %d: %s", resp.StatusCode, string(body))
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			break
		}
		if attempt < 2 {
			time.Sleep(2 * time.Second)
		}
	}
	return lastErr
}

const (
	maxConsecutivePollErrors = 5
)

func waitForSessionIdle(ctx context.Context, baseURL, sessionID, secret string, timeout time.Duration, idleCh <-chan struct{}) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	consecutiveErrors := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idleCh:
			slog.Info("session idle signal received from SSE events", "sessionID", sessionID)
			return nil
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("session %s did not finish within %v", sessionID, timeout)
			}
			status, err := getSessionStatus(ctx, baseURL, sessionID, secret)
			if err != nil {
				consecutiveErrors++
				slog.Warn("failed to poll session status", "sessionID", sessionID, "err", err, "consecutive", consecutiveErrors)
				if consecutiveErrors >= maxConsecutivePollErrors {
					return fmt.Errorf("polling session %s failed %d consecutive times: %w", sessionID, consecutiveErrors, err)
				}
				continue
			}
			consecutiveErrors = 0
			if status == "idle" || status == "completed" || status == "finished" || status == "done" {
				if status != "idle" {
					slog.Info("session status indicates completion", "sessionID", sessionID, "status", status)
				}
				return nil
			}
		}
	}
}

func getSessionStatus(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/session/status", nil)
	if err != nil {
		return "", err
	}
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GET /session/status: status %d", resp.StatusCode)
	}
	var statuses map[string]struct {
		Type string `json:"type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&statuses); err != nil {
		return "", err
	}
	s, ok := statuses[sessionID]
	if !ok {
		return "idle", nil
	}
	return s.Type, nil
}

func fetchSessionSummary(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/session/"+sessionID+"/message", nil)
	if err != nil {
		return "", err
	}
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET /message: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GET /message: status %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)

	var messages []struct {
		Info struct {
			Role string `json:"role"`
		} `json:"info"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(body, &messages); err != nil {
		return "", fmt.Errorf("parse messages: %w", err)
	}

	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Info.Role == "assistant" {
			var lines []string
			for _, part := range messages[i].Parts {
				if part.Type == "text" && part.Text != "" {
					lines = append(lines, part.Text)
				}
			}
			if len(lines) > 0 {
				return strings.Join(lines, "\n"), nil
			}
		}
	}
	return "", fmt.Errorf("no assistant response found in session %s messages", sessionID)
}

func opencodeServeCommand(port int) []string {
	return []string{"opencode", "serve", "--hostname", "0.0.0.0", "--port", strconv.Itoa(port)}
}

func opencodeServeArgs(port int) []string {
	return []string{"serve", "--hostname", "0.0.0.0", "--port", strconv.Itoa(port)}
}

func LogMCPStatus(ctx context.Context, baseURL, secret string) {
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/mcp", nil)
	if err != nil {
		slog.Warn("mcp status request failed", "err", err)
		return
	}
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("mcp status fetch failed", "err", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	slog.Info("opencode MCP server status", "status", resp.StatusCode, "body", string(body))
}

func abortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	abortCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := doPost(abortCtx, baseURL+"/session/"+sessionID+"/abort", "application/json", secret, strings.NewReader("{}"))
	if err != nil {
		return fmt.Errorf("POST /session/%s/abort: %w", sessionID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		return fmt.Errorf("POST /session/%s/abort: status %d: %s", sessionID, resp.StatusCode, string(body))
	}
	slog.Info("session aborted", "sessionID", sessionID)
	return nil
}
