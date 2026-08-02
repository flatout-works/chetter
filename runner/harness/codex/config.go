package codex

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/flatout-works/chetter/runner/harness/mcpconfig"
	"github.com/flatout-works/chetter/runner/internal/task"
)

func GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest) error {
	codexDir := wsDir + "/.codex"
	if err := os.MkdirAll(codexDir, 0750); err != nil {
		return err
	}

	_, model := codexModelFields(req)
	baseURL := strings.TrimSpace(req.ProviderBaseURL)
	apiKeyEnv := strings.TrimSpace(req.ProviderAPIKeyEnv)
	providerKind := strings.TrimSpace(req.Env["__chetter_provider_kind"])
	awsProfile := strings.TrimSpace(req.Env["__chetter_aws_profile"])
	awsRegion := strings.TrimSpace(req.Env["__chetter_aws_region"])

	if providerKind == "aws_bedrock" {
		return generateBedrockConfig(codexDir, model, baseURL, apiKeyEnv, awsProfile, awsRegion, runnerMCPURL, req.RunnerMCPToken, chetterMCPURL, chetterMCPToken, req.McpEndpoints)
	}
	// The responses wire API is the default (OpenAI, and providers such as
	// DeepSeek that implement /responses for Codex). Providers that only
	// speak chat completions opt in via a codex harness mapping with
	// api: openai-completions in the model catalog.
	wireAPI := "responses"
	switch strings.TrimSpace(req.ProviderAPI) {
	case "openai-completions", "chat", "chat-completions":
		wireAPI = "chat"
	}
	return generateNativeConfig(codexDir, model, baseURL, apiKeyEnv, wireAPI, runnerMCPURL, req.RunnerMCPToken, chetterMCPURL, chetterMCPToken, req.McpEndpoints)
}

func generateNativeConfig(codexDir, model, baseURL, apiKeyEnv, wireAPI, runnerMCPURL, runnerMCPToken, chetterMCPURL, chetterMCPToken string, endpoints []task.MCPEndpoint) error {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	if apiKeyEnv == "" {
		apiKeyEnv = "OPENAI_API_KEY"
	}
	if wireAPI == "" {
		wireAPI = "responses"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "model = %s\n", tomlString(model))
	b.WriteString("model_provider = \"chetter\"\n")
	b.WriteString("approval_policy = \"never\"\n")
	b.WriteString("sandbox_mode = \"workspace-write\"\n\n")
	b.WriteString("[model_providers.chetter]\n")
	b.WriteString("name = \"Chetter\"\n")
	fmt.Fprintf(&b, "base_url = %s\n", tomlString(baseURL))
	fmt.Fprintf(&b, "env_key = %s\n", tomlString(apiKeyEnv))
	fmt.Fprintf(&b, "wire_api = %s\n\n", tomlString(wireAPI))
	writeMCPServer(&b, "runner-bridge", runnerMCPURL, runnerMCPToken)
	writeMCPServer(&b, "chetter", chetterMCPURL, chetterMCPToken)
	writeEndpointMCPServers(&b, endpoints)
	return mcpconfig.WritePrivateFile(codexDir+"/config.toml", []byte(b.String()))
}

func generateBedrockConfig(codexDir, model, baseURL, apiKeyEnv, awsProfile, awsRegion, runnerMCPURL, runnerMCPToken, chetterMCPURL, chetterMCPToken string, endpoints []task.MCPEndpoint) error {
	if baseURL == "" {
		baseURL = "https://bedrock-runtime.us-east-1.amazonaws.com"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "model = %s\n", tomlString(model))
	b.WriteString("model_provider = \"amazon-bedrock\"\n")
	b.WriteString("approval_policy = \"never\"\n")
	b.WriteString("sandbox_mode = \"workspace-write\"\n\n")
	b.WriteString("[model_providers.amazon-bedrock]\n")
	b.WriteString("name = \"Amazon Bedrock\"\n")
	fmt.Fprintf(&b, "base_url = %s\n", tomlString(baseURL))
	b.WriteString("wire_api = \"responses\"\n\n")
	b.WriteString("[model_providers.amazon-bedrock.aws]\n")
	if awsProfile != "" {
		fmt.Fprintf(&b, "profile = %s\n", tomlString(awsProfile))
	}
	if awsRegion != "" {
		fmt.Fprintf(&b, "region = %s\n", tomlString(awsRegion))
	}
	b.WriteByte('\n')
	writeMCPServer(&b, "runner-bridge", runnerMCPURL, runnerMCPToken)
	writeMCPServer(&b, "chetter", chetterMCPURL, chetterMCPToken)
	writeEndpointMCPServers(&b, endpoints)
	return mcpconfig.WritePrivateFile(codexDir+"/config.toml", []byte(b.String()))
}

func writeMCPServer(b *strings.Builder, name, url, token string) {
	if strings.TrimSpace(url) == "" {
		return
	}
	fmt.Fprintf(b, "[mcp_servers.%s]\n", name)
	fmt.Fprintf(b, "url = %s\n", tomlString(url))
	b.WriteString("default_tools_approval_mode = \"approve\"\n")
	if token != "" {
		fmt.Fprintf(b, "http_headers = { Authorization = %s }\n", tomlString("Bearer "+token))
	}
	b.WriteByte('\n')
}

func writeEndpointMCPServers(b *strings.Builder, endpoints []task.MCPEndpoint) {
	for _, endpoint := range endpoints {
		if strings.TrimSpace(endpoint.URL) == "" {
			continue
		}
		fmt.Fprintf(b, "[mcp_servers.%s]\n", endpoint.Name)
		fmt.Fprintf(b, "url = %s\n", tomlString(endpoint.URL))
		b.WriteString("default_tools_approval_mode = \"approve\"\n")
		if endpoint.BearerTokenEnv != "" {
			fmt.Fprintf(b, "bearer_token_env_var = %s\n", tomlString(endpoint.BearerTokenEnv))
		}
		b.WriteByte('\n')
	}
}

func tomlString(value string) string { return strconv.Quote(value) }

func codexEnv(wsDir, secret string) map[string]string {
	return map[string]string{
		"CODEX_HOME":              wsDir + "/.codex",
		"CODEX_SERVE_PROXY_TOKEN": secret,
	}
}

func codexModelFields(req task.TaskRequest) (provider, model string) {
	provider = strings.TrimSpace(req.ProviderID)
	model = strings.TrimSpace(req.ModelID)
	if provider == "" {
		provider = "openai"
	}
	if model == "" {
		model = "gpt-5.4"
	}
	return provider, model
}

func resolvedModelID(req task.TaskRequest) string {
	provider, model := codexModelFields(req)
	return provider + "/" + model
}
