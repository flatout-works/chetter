package mcpconfig

import (
	"fmt"
	"net/url"
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
	if err := rejectCredentialedToolAllowlists(profiles, "OpenCode MCP config"); err != nil {
		return err
	}
	return addServers(mcpServers, profiles, func(p task.MCPProfile) map[string]any {
		return map[string]any{
			"type":    openCodeType(p),
			"url":     p.URL,
			"enabled": true,
		}
	})
}

func RejectToolAllowlistsForURL(profiles []task.MCPProfile, rawURL, target string) error {
	rawURL = normalizedProfileURL(rawURL)
	if rawURL == "" {
		return nil
	}
	for _, profile := range profiles {
		if len(nonEmptyStrings(profile.ToolAllowlist)) == 0 {
			continue
		}
		if normalizedProfileURL(profile.URL) != rawURL {
			continue
		}
		return toolAllowlistCredentialExposureError(profile, target)
	}
	return nil
}

func AddHTTPServers(mcpServers map[string]any, profiles []task.MCPProfile) error {
	if err := rejectToolAllowlists(profiles, "HTTP MCP config"); err != nil {
		return err
	}
	return addServers(mcpServers, profiles, func(p task.MCPProfile) map[string]any {
		return map[string]any{
			"type":    httpType(p),
			"url":     p.URL,
			"enabled": true,
		}
	})
}

func AddPiServers(mcpServers map[string]any, profiles []task.MCPProfile) error {
	if err := rejectToolAllowlists(profiles, "Pi MCP config"); err != nil {
		return err
	}
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
		tools := nonEmptyStrings(profile.ToolAllowlist)
		if len(tools) == 0 {
			continue
		}
		AddOpenCodeToolPermission(perms, name, "*", "deny")
		for _, tool := range tools {
			AddOpenCodeToolPermission(perms, name, tool, "allow")
		}
	}
}

func AddOpenCodeToolPermission(perms map[string]any, serverName, tool, action string) {
	serverName = strings.TrimSpace(serverName)
	tool = strings.TrimSpace(tool)
	action = strings.TrimSpace(action)
	if serverName == "" || tool == "" || action == "" {
		return
	}
	perms[serverName+"_"+tool] = action
	perms["mcp__"+serverName+"__"+tool] = action
}

func rejectToolAllowlists(profiles []task.MCPProfile, target string) error {
	for _, profile := range profiles {
		if len(nonEmptyStrings(profile.ToolAllowlist)) > 0 {
			name := strings.TrimSpace(profile.Name)
			if name == "" {
				name = "<unnamed>"
			}
			return fmt.Errorf("mcp profile %q declares tool_allowlist, but %s cannot enforce per-tool MCP restrictions", name, target)
		}
	}
	return nil
}

func rejectCredentialedToolAllowlists(profiles []task.MCPProfile, target string) error {
	for _, profile := range profiles {
		if len(nonEmptyStrings(profile.ToolAllowlist)) == 0 {
			continue
		}
		if !ProfileCarriesCredentials(profile) {
			continue
		}
		return toolAllowlistCredentialExposureError(profile, target)
	}
	return nil
}

func toolAllowlistCredentialExposureError(profile task.MCPProfile, target string) error {
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = "<unnamed>"
	}
	return fmt.Errorf("mcp profile %q declares tool_allowlist, but %s would expose unrestricted credentials in task-readable config", name, target)
}

func ProfileCarriesCredentials(profile task.MCPProfile) bool {
	if len(nonEmptyHeaders(profile.Headers)) > 0 {
		return true
	}
	return profileURLCarriesCredentials(profile.URL)
}

func nonEmptyHeaders(headers map[string]string) []string {
	out := make([]string, 0, len(headers))
	for key, value := range headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			out = append(out, key)
		}
	}
	return out
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

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func normalizedProfileURL(rawURL string) string {
	return strings.TrimRight(strings.TrimSpace(rawURL), "/")
}

func profileURLCarriesCredentials(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return true
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return true
	}
	if parsed.User != nil {
		return true
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return true
	}
	if parsed.RawQuery != "" || parsed.ForceQuery {
		return true
	}
	fragment := parsed.RawFragment
	if fragment == "" {
		fragment = parsed.Fragment
	}
	if strings.TrimSpace(fragment) != "" {
		return true
	}
	return !publicMCPURLPath(parsed.Path)
}

func publicMCPURLPath(pathValue string) bool {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" || pathValue == "/" {
		return true
	}
	if decoded, err := url.PathUnescape(pathValue); err == nil {
		pathValue = decoded
	}
	parts := strings.FieldsFunc(strings.ToLower(strings.Trim(pathValue, "/")), func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if len(parts) == 0 {
		return true
	}
	last := parts[len(parts)-1]
	if last != "mcp" && last != "sse" {
		return false
	}
	for _, part := range parts[:len(parts)-1] {
		switch part {
		case "api", "v1", "v2", "v3", "mcp":
		default:
			return false
		}
	}
	return true
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
