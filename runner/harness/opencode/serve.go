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
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/config", nil)
		if err != nil {
			time.Sleep(servePollInterval)
			continue
		}
		if secret != "" {
			req.Header.Set("Authorization", basicAuthHeader(secret))
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(servePollInterval)
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
	payload, _ := json.Marshal(map[string]any{
		"parts": []map[string]any{
			{"type": "text", "text": promptWithSkillHints(req.Prompt, req.Skills)},
		},
		"model": model,
	})

	url := baseURL + "/session/" + sessionID + "/message"
	httpClient := &http.Client{Timeout: timeout}
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payload))
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
	slog.Info("message response", "status", resp.StatusCode, "len", len(respBody))

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("POST /message: status %d: %s", resp.StatusCode, string(respBody))
	}

	var msg struct {
		Info struct {
			Role string `json:"role"`
		} `json:"info"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(respBody, &msg); err != nil {
		return "", fmt.Errorf("parse message response: %w", err)
	}
	var summaryLines []string
	for _, part := range msg.Parts {
		if part.Type == "text" {
			summaryLines = append(summaryLines, part.Text)
		}
	}
	return strings.Join(summaryLines, "\n"), nil
}

func exportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	exportCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	payload, _ := json.Marshal(map[string]any{
		"parts": []map[string]any{
			{"type": "text", "text": "/export"},
		},
	})
	url := baseURL + "/session/" + sessionID + "/message"
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
		return "", fmt.Errorf("POST /message /export: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("POST /message /export: status %d: %s", resp.StatusCode, string(respBody))
	}
	var msg struct {
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(respBody, &msg); err != nil {
		return "", fmt.Errorf("parse export response: %w", err)
	}
	var lines []string
	for _, part := range msg.Parts {
		if part.Type == "text" {
			lines = append(lines, part.Text)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func opencodeServeArgs(port int) []string {
	args := []string{"serve"}
	if !mem9Enabled() {
		args = append(args, "--pure")
	}
	return append(args, "--port", strconv.Itoa(port))
}
