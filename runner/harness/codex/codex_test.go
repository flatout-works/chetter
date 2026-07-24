package codex

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestGenerateConfig(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		ProviderID:        "openai",
		ModelID:           "gpt-5.4",
		ProviderBaseURL:   "https://api.example.test/v1",
		ProviderAPIKeyEnv: "EXAMPLE_API_KEY",
		McpEndpoints: []task.MCPEndpoint{{
			Name:           "docs",
			URL:            "https://docs.example.test/mcp",
			BearerTokenEnv: "DOCS_MCP_TOKEN",
		}},
	}
	if err := GenerateConfig(wsDir, "http://runner.test/mcp", "https://chetter.test/mcp", "secret", req); err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	data, err := os.ReadFile(wsDir + "/.codex/config.toml")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := string(data)
	for _, want := range []string{
		`model = "gpt-5.4"`,
		`model_provider = "chetter"`,
		`approval_policy = "never"`,
		`sandbox_mode = "workspace-write"`,
		`base_url = "https://api.example.test/v1"`,
		`env_key = "EXAMPLE_API_KEY"`,
		`wire_api = "responses"`,
		`[mcp_servers.runner-bridge]`,
		`[mcp_servers.chetter]`,
		`Authorization = "Bearer secret"`,
		`[mcp_servers.docs]`,
		`bearer_token_env_var = "DOCS_MCP_TOKEN"`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, `[mcp_servers.docs.http_headers]`) {
		t.Fatal("Codex endpoint bearer token must not be materialized as a static header")
	}
}

func TestWatchEventsSignalsDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: done\ndata: {\"status\":\"completed\",\"summary\":\"finished\"}\n\n")
	}))
	defer srv.Close()
	completed := make(chan struct{})
	watchEvents(context.Background(), "task", srv.URL, "", func(string, string) {}, nil, func(summary string, err error) {
		if err != nil || summary != "finished" {
			t.Errorf("completion = %q, %v", summary, err)
		}
		close(completed)
	})
	select {
	case <-completed:
	default:
		t.Fatal("done SSE did not signal completion")
	}
}

func TestSendPromptRecoversFromLostHTTPResponseViaCompletion(t *testing.T) {
	idle := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		once.Do(func() { close(idle) })
		<-time.After(time.Second)
	}))
	defer srv.Close()
	summary, err := sendPrompt(context.Background(), srv.URL, "session", "", task.TaskRequest{Prompt: "work"}, 2*time.Second, idle, func() (string, error, bool) {
		return "finished", nil, true
	})
	if err != nil || summary != "finished" {
		t.Fatalf("sendPrompt = %q, %v", summary, err)
	}
}

func TestSendPromptWaitsForTerminalEventAfterHTTPResponse(t *testing.T) {
	idle := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"summary":"http summary"}`)
	}))
	defer srv.Close()
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(idle)
	}()
	started := time.Now()
	summary, err := sendPrompt(context.Background(), srv.URL, "session", "", task.TaskRequest{Prompt: "work"}, 2*time.Second, idle, func() (string, error, bool) {
		return "terminal summary", nil, true
	})
	if err != nil || summary != "http summary" {
		t.Fatalf("sendPrompt = %q, %v", summary, err)
	}
	if time.Since(started) < 40*time.Millisecond {
		t.Fatal("SendPrompt returned before terminal usage could be delivered")
	}
}

func TestWatchEventsPropagatesTerminalFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: done\ndata: {\"status\":\"failed\",\"error\":\"app server exited\"}\n\n")
	}))
	defer srv.Close()
	var terminalErr error
	watchEvents(context.Background(), "task", srv.URL, "", func(string, string) {}, nil, func(_ string, err error) {
		terminalErr = err
	})
	if terminalErr == nil || !strings.Contains(terminalErr.Error(), "app server exited") {
		t.Fatalf("terminal error = %v", terminalErr)
	}
}

func TestResolvedModelIDDefaults(t *testing.T) {
	if got := resolvedModelID(task.TaskRequest{}); got != "openai/gpt-5.4" {
		t.Fatalf("resolved model = %q", got)
	}
}

func TestPromptWithSkillHints(t *testing.T) {
	if got := promptWithSkillHints("do work", []string{"go", "svelte"}); !strings.Contains(got, "go, svelte") || !strings.HasSuffix(got, "do work") {
		t.Fatalf("unexpected prompt: %q", got)
	}
}

func TestGenerateBedrockConfig(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{
		ProviderID:      "aws-bedrock",
		ModelID:         "us.anthropic.claude-sonnet-4-20250514-v1:0",
		ProviderBaseURL: "https://bedrock-runtime.us-west-2.amazonaws.com",
		Env: map[string]string{
			"__chetter_provider_kind": "aws_bedrock",
			"__chetter_aws_profile":   "chetter-bedrock",
			"__chetter_aws_region":    "us-west-2",
		},
	}
	if err := GenerateConfig(wsDir, "http://runner.test/mcp", "https://chetter.test/mcp", "secret", req); err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	data, err := os.ReadFile(wsDir + "/.codex/config.toml")
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := string(data)
	for _, want := range []string{
		`model = "us.anthropic.claude-sonnet-4-20250514-v1:0"`,
		`model_provider = "amazon-bedrock"`,
		`[model_providers.amazon-bedrock]`,
		`[model_providers.amazon-bedrock.aws]`,
		`profile = "chetter-bedrock"`,
		`region = "us-west-2"`,
		`base_url = "https://bedrock-runtime.us-west-2.amazonaws.com"`,
		`approval_policy = "never"`,
		`sandbox_mode = "workspace-write"`,
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, `model_provider = "chetter"`) {
		t.Fatal("Bedrock config should not contain 'chetter' model provider")
	}
}
