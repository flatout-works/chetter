package mcpconfig

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/flatout-works/chetter/runner/internal/task"
)

var envRefPattern = regexp.MustCompile(`\$\{env:([A-Za-z_][A-Za-z0-9_]*)\}`)

// addServers renders each profile into mcpServers, using build for the
// harness-specific per-server entry. It owns the shared pipeline: validate the
// profile, resolve its headers, attach them, and register under the normalized
// name.
func addServers(mcpServers map[string]any, profiles []task.MCPProfile, build func(task.MCPProfile) map[string]any) error {
	for _, profile := range profiles {
		normalized, err := normalize(profile)
		if err != nil {
			return err
		}
		headers, err := ResolveHeaders(normalized)
		if err != nil {
			return err
		}
		server := build(normalized)
		if len(headers) > 0 {
			server["headers"] = headers
		}
		mcpServers[normalized.Name] = server
	}
	return nil
}

func AddOpenCodeServers(mcpServers map[string]any, profiles []task.MCPProfile) error {
	return addServers(mcpServers, profiles, func(p task.MCPProfile) map[string]any {
		return map[string]any{
			"type":    openCodeType(p),
			"url":     p.URL,
			"enabled": true,
		}
	})
}

func AddHTTPServers(mcpServers map[string]any, profiles []task.MCPProfile) error {
	return addServers(mcpServers, profiles, func(p task.MCPProfile) map[string]any {
		return map[string]any{
			"type":    httpType(p),
			"url":     p.URL,
			"enabled": true,
		}
	})
}

func AddPiServers(mcpServers map[string]any, profiles []task.MCPProfile) error {
	return addServers(mcpServers, profiles, func(p task.MCPProfile) map[string]any {
		return map[string]any{
			"url":       p.URL,
			"lifecycle": "keep-alive",
		}
	})
}

func AddOpenCodePermissions(perms map[string]any, profiles []task.MCPProfile) {
	for _, profile := range profiles {
		name := strings.TrimSpace(profile.Name)
		if name == "" {
			continue
		}
		for _, tool := range profile.ToolAllowlist {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}
			perms["mcp__"+name+"__"+tool] = "allow"
		}
	}
}

func ResolveHeaders(profile task.MCPProfile) (map[string]string, error) {
	if len(profile.Headers) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(profile.Headers))
	for key, value := range profile.Headers {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		resolved, err := resolveEnvRefs(profile.Name, key, value)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(resolved) != "" {
			out[key] = resolved
		}
	}
	return out, nil
}

func normalize(profile task.MCPProfile) (task.MCPProfile, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.URL = strings.TrimSpace(profile.URL)
	profile.Type = strings.TrimSpace(profile.Type)
	profile.Transport = strings.TrimSpace(profile.Transport)
	if profile.Name == "" {
		return profile, fmt.Errorf("mcp profile name is required")
	}
	if strings.EqualFold(profile.Name, "runner-bridge") || strings.EqualFold(profile.Name, "chetter") {
		return profile, fmt.Errorf("mcp profile %q uses a reserved server name", profile.Name)
	}
	if profile.URL == "" {
		return profile, fmt.Errorf("mcp profile %q url is required", profile.Name)
	}
	return profile, nil
}

func resolveEnvRefs(profileName, headerName, value string) (string, error) {
	var missing []string
	resolved := envRefPattern.ReplaceAllStringFunc(value, func(match string) string {
		parts := envRefPattern.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		envName := parts[1]
		envValue, ok := os.LookupEnv(envName)
		if !ok || envValue == "" {
			missing = append(missing, envName)
			return ""
		}
		return envValue
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("mcp profile %q header %q references missing env %s", profileName, headerName, strings.Join(missing, ", "))
	}
	return resolved, nil
}

func openCodeType(profile task.MCPProfile) string {
	if profile.Type != "" {
		return profile.Type
	}
	return "remote"
}

func httpType(profile task.MCPProfile) string {
	if profile.Type != "" && profile.Type != "remote" {
		return profile.Type
	}
	if profile.Transport != "" {
		return profile.Transport
	}
	return "http"
}
