package codewhale

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/flatout-works/chetter/runner/harness/mcpconfig"
	"github.com/flatout-works/chetter/runner/internal/task"
)

func GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	codewhaleDir := wsDir + "/.codewhale"
	if err := os.MkdirAll(codewhaleDir, 0750); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(req.ProviderID), "synthetic") {
		baseURL := strings.TrimSpace(req.ProviderBaseURL)
		apiKeyEnv := strings.TrimSpace(req.ProviderAPIKeyEnv)
		_, model := codewhaleModelFields(req)
		if baseURL == "" || apiKeyEnv == "" {
			return fmt.Errorf("configure CodeWhale Synthetic provider: base URL and API key environment are required")
		}
		providerConfig := fmt.Sprintf("[providers.synthetic]\nkind = %q\nbase_url = %q\nmodel = %q\napi_key_env = %q\n",
			"openai-compatible", baseURL, model, apiKeyEnv)
		if err := mcpconfig.WritePrivateFile(codewhaleDir+"/chetter-config.toml", []byte(providerConfig)); err != nil {
			return err
		}
	}

	servers := map[string]any{}

	if runnerMCPURL != "" {
		runnerMCP := map[string]any{
			"url":     runnerMCPURL,
			"enabled": true,
		}
		mcpconfig.SetBearerToken(runnerMCP, req.RunnerMCPToken)
		servers["runner-bridge"] = runnerMCP
	}

	if chetterMCPURL != "" {
		chetterMCP := map[string]any{
			"url":     chetterMCPURL,
			"enabled": true,
		}
		mcpconfig.SetBearerToken(chetterMCP, chetterMCPToken)
		servers["chetter"] = chetterMCP
	}

	if len(req.McpEndpoints) > 0 {
		if err := mcpconfig.AddCodeWhaleServers(servers, req.McpEndpoints); err != nil {
			return err
		}
	}

	if len(servers) > 0 {
		agentMCP := map[string]any{
			"servers": servers,
		}
		agentMCPData, err := json.MarshalIndent(agentMCP, "", "  ")
		if err != nil {
			return err
		}
		agentMCPPath := codewhaleDir + "/mcp.json"
		if err := mcpconfig.WritePrivateFile(agentMCPPath, agentMCPData); err != nil {
			return err
		}
		slog.Info("wrote codewhale mcp config", "path", agentMCPPath)
	}

	return nil
}

func codewhaleEnv(wsDir, secret string, req task.TaskRequest) map[string]string {
	provider, model := codewhaleModelFields(req)
	env := map[string]string{
		"CODEWHALE_CONFIG_DIR":    wsDir + "/.codewhale",
		"CODEWHALE_OFFLINE":       "1",
		"CODEWHALE_RUNTIME_TOKEN": secret,
		"CODEWHALE_PROVIDER":      provider,
		"CODEWHALE_MODEL":         model,
	}

	provider = strings.ToLower(strings.TrimSpace(provider))
	if baseURL := strings.TrimSpace(req.ProviderBaseURL); baseURL != "" {
		env["CODEWHALE_BASE_URL"] = baseURL
	}
	if provider == "synthetic" {
		env["CODEWHALE_CONFIG_PATH"] = wsDir + "/.codewhale/chetter-config.toml"
	}
	if provider == "deepseek" {
		baseURL := strings.TrimSpace(req.ProviderBaseURL)
		apiKeyEnv := strings.TrimSpace(req.ProviderAPIKeyEnv)
		if baseURL == "" {
			baseURL = "https://api.deepseek.com"
		}
		if apiKeyEnv == "" {
			apiKeyEnv = "DEEPSEEK_API_KEY"
		}
		env["CODEWHALE_BASE_URL"] = baseURL
		if apiKey := os.Getenv(apiKeyEnv); apiKey != "" {
			env["DEEPSEEK_API_KEY"] = apiKey
		}
	}

	return env
}
