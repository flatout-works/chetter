package mcpconfig

import (
	"fmt"
	"strings"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func AddOpenCodeServers(servers map[string]any, profiles []task.MCPProfile) error {
	for _, profile := range profiles {
		headers, err := profileHeaders(profile, func(name string) string { return "{env:" + name + "}" })
		if err != nil {
			return err
		}
		server := map[string]any{"type": "remote", "url": profile.URL, "enabled": true}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		servers[profile.Name] = server
	}
	return nil
}

func AddClaudeServers(servers map[string]any, profiles []task.MCPProfile) error {
	for _, profile := range profiles {
		headers, err := profileHeaders(profile, shellEnvReference)
		if err != nil {
			return err
		}
		server := map[string]any{"type": profileTransport(profile), "url": profile.URL}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		servers[profile.Name] = server
	}
	return nil
}

func AddCodeWhaleServers(servers map[string]any, profiles []task.MCPProfile) error {
	for _, profile := range profiles {
		headers, err := profileHeaders(profile, nil)
		if err != nil {
			return err
		}
		server := map[string]any{"url": profile.URL, "enabled": true}
		if profileTransport(profile) == "sse" {
			server["transport"] = "sse"
		}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		if profile.BearerTokenEnv != "" {
			server["bearer_token_env_var"] = profile.BearerTokenEnv
		}
		servers[profile.Name] = server
	}
	return nil
}

func AddPiServers(servers map[string]any, profiles []task.MCPProfile) error {
	for _, profile := range profiles {
		headers, err := profileHeaders(profile, nil)
		if err != nil {
			return err
		}
		server := map[string]any{"url": profile.URL, "lifecycle": "keep-alive"}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		if profile.BearerTokenEnv != "" {
			server["auth"] = "bearer"
			server["bearerTokenEnv"] = profile.BearerTokenEnv
		}
		servers[profile.Name] = server
	}
	return nil
}

func profileHeaders(profile task.MCPProfile, envReference func(string) string) (map[string]string, error) {
	if strings.TrimSpace(profile.Name) == "" || strings.TrimSpace(profile.URL) == "" {
		return nil, fmt.Errorf("mcp profile %q is incomplete", profile.Name)
	}
	headers := make(map[string]string, len(profile.Headers)+1)
	for key, value := range profile.Headers {
		if strings.EqualFold(key, "Authorization") {
			return nil, fmt.Errorf("mcp profile %q must use bearer_token_env for authorization", profile.Name)
		}
		headers[key] = value
	}
	if profile.BearerTokenEnv != "" && envReference != nil {
		headers["Authorization"] = "Bearer " + envReference(profile.BearerTokenEnv)
	}
	return headers, nil
}

func profileTransport(profile task.MCPProfile) string {
	if profile.Transport == "sse" {
		return "sse"
	}
	return "http"
}

func shellEnvReference(name string) string {
	return "${" + name + "}"
}
