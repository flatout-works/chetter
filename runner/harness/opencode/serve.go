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

	"github.com/flatout-works/chetter/runner/internal/task"
)

const (
	serveReadyTimeout = 15 * time.Second
	servePollInterval = 500 * time.Millisecond
	serveHTTPTimeout  = 2 * time.Second
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

func sendPromptAndWait(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, wsDir string, timeout time.Duration) (string, error) {
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
			{"type": "text", "text": promptWithSkillHints(req.Prompt, req.Skills)},
		},
		"model": model,
	})

	if err := startAsyncPrompt(ctx, baseURL, sessionID, secret, payload); err != nil {
		return "", err
	}

	if err := waitForSessionIdle(ctx, baseURL, sessionID, secret, timeout); err != nil {
		return "", err
	}

	return fetchSessionSummary(ctx, baseURL, sessionID, secret)
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
		resp.Body.Close()
		if resp.StatusCode == 204 || resp.StatusCode == 200 {
			return nil
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
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

func waitForSessionIdle(ctx context.Context, baseURL, sessionID, secret string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("session %s did not finish within %v", sessionID, timeout)
			}
			status, err := getSessionStatus(ctx, baseURL, sessionID, secret)
			if err != nil {
				slog.Warn("failed to poll session status", "sessionID", sessionID, "err", err)
				continue
			}
			if status == "idle" {
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

func exportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	exportCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	payload, _ := json.Marshal(map[string]any{
		"parts": []map[string]any{
			{"type": "text", "text": "/export"},
		},
	})
	url := baseURL + "/session/" + sessionID + "/prompt_async"
	httpClient := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(exportCtx, "POST", url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create export request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("POST /prompt_async /export: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 && resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		return "", fmt.Errorf("POST /prompt_async /export: status %d: %s", resp.StatusCode, string(body))
	}

	if err := waitForSessionIdle(exportCtx, baseURL, sessionID, secret, 25*time.Second); err != nil {
		return "", fmt.Errorf("export wait: %w", err)
	}

	var messages []struct {
		Info struct {
			Role string `json:"role"`
		} `json:"info"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	msgReq, _ := http.NewRequestWithContext(exportCtx, "GET", baseURL+"/session/"+sessionID+"/message", nil)
	if secret != "" {
		msgReq.Header.Set("Authorization", basicAuthHeader(secret))
	}
	msgResp, err := httpClient.Do(msgReq)
	if err != nil {
		return "", fmt.Errorf("GET /message /export: %w", err)
	}
	defer msgResp.Body.Close()
	if msgResp.StatusCode != 200 {
		return "", fmt.Errorf("GET /message /export: status %d", msgResp.StatusCode)
	}
	respBody, _ := io.ReadAll(msgResp.Body)
	if err := json.Unmarshal(respBody, &messages); err != nil {
		return "", fmt.Errorf("parse export response: %w", err)
	}
	var lines []string
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Info.Role == "assistant" {
			for _, part := range messages[i].Parts {
				if part.Type == "text" {
					lines = append(lines, part.Text)
				}
			}
			break
		}
	}
	return strings.Join(lines, "\n"), nil
}

func opencodeServeCommand(port int) []string {
	return []string{"opencode", "serve", "--hostname", "0.0.0.0", "--port", strconv.Itoa(port)}
}

func opencodeServeArgs(port int) []string {
	return []string{"serve", "--hostname", "0.0.0.0", "--port", strconv.Itoa(port)}
}

func opencodeServeArgsResume(port int) []string {
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
