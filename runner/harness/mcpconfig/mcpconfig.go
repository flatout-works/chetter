package mcpconfig

import (
	"fmt"
	"strings"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func AddOpenCodeServers(servers map[string]any, endpoints []task.MCPEndpoint) error {
	for _, endpoint := range endpoints {
		headers, err := endpointHeaders(endpoint, func(name string) string { return "{env:" + name + "}" })
		if err != nil {
			return err
		}
		server := map[string]any{"type": "remote", "url": endpoint.URL, "enabled": true}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		servers[endpoint.Name] = server
	}
	return nil
}

func AddClaudeServers(servers map[string]any, endpoints []task.MCPEndpoint) error {
	for _, endpoint := range endpoints {
		headers, err := endpointHeaders(endpoint, shellEnvReference)
		if err != nil {
			return err
		}
		server := map[string]any{"type": endpointTransport(endpoint), "url": endpoint.URL}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		servers[endpoint.Name] = server
	}
	return nil
}

func AddCodeWhaleServers(servers map[string]any, endpoints []task.MCPEndpoint) error {
	for _, endpoint := range endpoints {
		headers, err := endpointHeaders(endpoint, nil)
		if err != nil {
			return err
		}
		server := map[string]any{"url": endpoint.URL, "enabled": true}
		if endpointTransport(endpoint) == "sse" {
			server["transport"] = "sse"
		}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		if endpoint.BearerTokenEnv != "" {
			server["bearer_token_env_var"] = endpoint.BearerTokenEnv
		}
		servers[endpoint.Name] = server
	}
	return nil
}

func AddPiServers(servers map[string]any, endpoints []task.MCPEndpoint) error {
	for _, endpoint := range endpoints {
		headers, err := endpointHeaders(endpoint, nil)
		if err != nil {
			return err
		}
		server := map[string]any{"url": endpoint.URL, "lifecycle": "keep-alive"}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		if endpoint.BearerTokenEnv != "" {
			server["auth"] = "bearer"
			server["bearerTokenEnv"] = endpoint.BearerTokenEnv
		}
		servers[endpoint.Name] = server
	}
	return nil
}

func endpointHeaders(endpoint task.MCPEndpoint, envReference func(string) string) (map[string]string, error) {
	if strings.TrimSpace(endpoint.Name) == "" || strings.TrimSpace(endpoint.URL) == "" {
		return nil, fmt.Errorf("mcp endpoint %q is incomplete", endpoint.Name)
	}
	headers := make(map[string]string, len(endpoint.Headers)+1)
	for key, value := range endpoint.Headers {
		if strings.EqualFold(key, "Authorization") {
			return nil, fmt.Errorf("mcp endpoint %q must use bearer_token_env for authorization", endpoint.Name)
		}
		headers[key] = value
	}
	if endpoint.BearerTokenEnv != "" && envReference != nil {
		headers["Authorization"] = "Bearer " + envReference(endpoint.BearerTokenEnv)
	}
	return headers, nil
}

func endpointTransport(endpoint task.MCPEndpoint) string {
	if endpoint.Transport == "sse" {
		return "sse"
	}
	return "http"
}

func shellEnvReference(name string) string {
	return "${" + name + "}"
}
