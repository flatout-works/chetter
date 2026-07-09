package mcpconfig

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func AddOpenCodeServers(servers map[string]any, profiles []task.MCPProfile) error {
	for _, profile := range profiles {
		normalized, headers, err := resolve(profile)
		if err != nil {
			return err
		}
		server := map[string]any{
			"type":    "remote",
			"url":     normalized.URL,
			"enabled": true,
		}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		servers[normalized.Name] = server
	}
	return nil
}

func AddCodeWhaleServers(servers map[string]any, profiles []task.MCPProfile) error {
	for _, profile := range profiles {
		normalized, headers, err := resolve(profile)
		if err != nil {
			return err
		}
		server := map[string]any{
			"type":    normalized.Transport,
			"url":     normalized.URL,
			"enabled": true,
		}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		servers[normalized.Name] = server
	}
	return nil
}

func AddClaudeServers(servers map[string]any, profiles []task.MCPProfile) error {
	for _, profile := range profiles {
		normalized, headers, err := resolve(profile)
		if err != nil {
			return err
		}
		server := map[string]any{
			"type": normalized.Transport,
			"url":  normalized.URL,
		}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		servers[normalized.Name] = server
	}
	return nil
}

func AddPiServers(servers map[string]any, profiles []task.MCPProfile) error {
	for _, profile := range profiles {
		normalized, headers, err := resolve(profile)
		if err != nil {
			return err
		}
		server := map[string]any{
			"url":       normalized.URL,
			"lifecycle": "keep-alive",
		}
		if len(headers) > 0 {
			server["headers"] = headers
		}
		servers[normalized.Name] = server
	}
	return nil
}

func resolve(profile task.MCPProfile) (task.MCPProfile, map[string]string, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Transport = strings.ToLower(strings.TrimSpace(profile.Transport))
	profile.URL = strings.TrimSpace(profile.URL)
	profile.BearerTokenEnv = strings.TrimSpace(profile.BearerTokenEnv)
	if profile.Name == "" {
		return profile, nil, fmt.Errorf("mcp profile name is required")
	}
	if !validProfileName(profile.Name) {
		return profile, nil, fmt.Errorf("mcp profile name must start with a letter or number, contain only letters, numbers, dot, underscore, or dash, and be at most 128 characters")
	}
	if strings.EqualFold(profile.Name, "runner-bridge") || strings.EqualFold(profile.Name, "chetter") {
		return profile, nil, fmt.Errorf("mcp profile %q uses a reserved server name", profile.Name)
	}
	if profile.URL == "" {
		return profile, nil, fmt.Errorf("mcp profile %q url is required", profile.Name)
	}
	parsedURL, err := url.Parse(profile.URL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") || parsedURL.User != nil || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		return profile, nil, fmt.Errorf("mcp profile %q url must be an absolute http or https URL without credentials, query parameters, or fragments", profile.Name)
	}
	if profile.Transport == "" {
		profile.Transport = "http"
	}
	if profile.Transport != "http" && profile.Transport != "sse" {
		return profile, nil, fmt.Errorf("mcp profile %q transport must be http or sse", profile.Name)
	}

	headers := make(map[string]string, len(profile.Headers)+1)
	seenHeaders := make(map[string]string, len(profile.Headers))
	for key, value := range profile.Headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return profile, nil, fmt.Errorf("mcp profile %q has an empty header name or value", profile.Name)
		}
		if strings.EqualFold(key, "Authorization") {
			return profile, nil, fmt.Errorf("mcp profile %q must fill bearer authorization from bearer_token_env", profile.Name)
		}
		lookup := strings.ToLower(key)
		if previous, ok := seenHeaders[lookup]; ok {
			return profile, nil, fmt.Errorf("mcp profile %q has duplicate headers %q and %q", profile.Name, previous, key)
		}
		seenHeaders[lookup] = key
		headers[key] = value
	}
	if profile.BearerTokenEnv != "" {
		token, ok := os.LookupEnv(profile.BearerTokenEnv)
		if !ok || strings.TrimSpace(token) == "" {
			return profile, nil, fmt.Errorf("mcp profile %q references missing bearer token env %s", profile.Name, profile.BearerTokenEnv)
		}
		headers["Authorization"] = "Bearer " + token
	}
	return profile, headers, nil
}

func validProfileName(name string) bool {
	if name == "" || len(name) > 128 || !asciiAlphaNumeric(name[0]) {
		return false
	}
	for _, value := range name {
		if value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9' || value == '.' || value == '_' || value == '-' {
			continue
		}
		return false
	}
	return true
}

func asciiAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}
