package codex

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
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

const (
	servePollInterval = 500 * time.Millisecond
	serveHTTPTimeout  = 2 * time.Second
)

func generatePassword() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func basicAuthHeader(secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("opencode:"+secret))
}

func doPost(ctx context.Context, url, secret string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
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
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/config", nil)
		if err == nil {
			if secret != "" {
				req.Header.Set("Authorization", basicAuthHeader(secret))
			}
			resp, err := client.Do(req)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					return nil
				}
			} else {
				lastErr = err
			}
		} else {
			lastErr = err
		}
		time.Sleep(servePollInterval)
	}
	if lastErr != nil {
		return fmt.Errorf("server at %s not responding within %v: %w", baseURL, timeout, lastErr)
	}
	return fmt.Errorf("server at %s not responding within %v", baseURL, timeout)
}

func createSession(ctx context.Context, baseURL, secret string) (string, error) {
	resp, err := doPost(ctx, baseURL+"/session", secret, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return "", fmt.Errorf("POST /session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST /session: status %d: %s", resp.StatusCode, body)
	}
	var result struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode session response: %w", err)
	}
	if result.SessionID == "" {
		return "", fmt.Errorf("POST /session returned no session ID")
	}
	return result.SessionID, nil
}

func sendPrompt(ctx context.Context, baseURL, sessionID, secret string, req task.TaskRequest, timeout time.Duration) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"prompt": promptWithSkillHints(req.Prompt, req.Skills),
		"model":  codexModel(req),
		"resume": req.ResumeHarnessSessionID != "",
		"agent":  req.Agent,
	})
	httpClient := &http.Client{Timeout: timeout}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/session/"+sessionID+"/message", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if secret != "" {
		httpReq.Header.Set("Authorization", basicAuthHeader(secret))
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("POST /message: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("POST /message: status %d: %s", resp.StatusCode, body)
	}
	var result struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("decode prompt response: %w", err)
	}
	return result.Summary, nil
}

func abortSession(ctx context.Context, baseURL, sessionID, secret string) error {
	resp, err := doPost(ctx, baseURL+"/session/"+sessionID+"/abort", secret, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return fmt.Errorf("POST /abort: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST /abort: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

func exportSession(ctx context.Context, baseURL, sessionID, secret string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/session/"+sessionID+"/export", nil)
	if err != nil {
		return "", err
	}
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("GET /export: status %d: %s", resp.StatusCode, body)
	}
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func watchEvents(ctx context.Context, taskID, baseURL, secret string, publishFn func(status, message string), tokenFn func(usage task.TokenUsage)) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/event", nil)
	if err != nil {
		return
	}
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	var eventType string
	var data strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if eventType == "codex.delta" && data.Len() > 0 {
				publishFn("running", "codex: "+data.String())
			} else if eventType == "codex.activity" && data.Len() > 0 {
				publishFn("running", "codex: "+data.String())
			} else if eventType == "codex.usage" && tokenFn != nil {
				var usage task.TokenUsage
				if json.Unmarshal([]byte(data.String()), &usage) == nil {
					tokenFn(usage)
				}
			}
			eventType = ""
			data.Reset()
			continue
		}
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
		} else if strings.HasPrefix(line, "data: ") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(line, "data: "))
		}
	}
}

func codexServeCommand(port int) []string {
	return []string{"codex-serve-proxy", "--port", strconv.Itoa(port)}
}

func codexModel(req task.TaskRequest) string {
	_, model := codexModelFields(req)
	return model
}

func promptWithSkillHints(prompt string, skills []string) string {
	if len(skills) == 0 {
		return prompt
	}
	return fmt.Sprintf("You have access to the following skills: %s. Use them when relevant.\n\n%s", strings.Join(skills, ", "), prompt)
}
