package opencode

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const defaultMem9PluginSpec = "@mem9/opencode"

func mem9Enabled() bool {
	return strings.TrimSpace(os.Getenv("MEM9_API_KEY")) != ""
}

func mem9PluginSpec() string {
	if spec := strings.TrimSpace(os.Getenv("MEM9_PLUGIN_SPEC")); spec != "" {
		return spec
	}
	return defaultMem9PluginSpec
}

func ensureMem9Plugin(cfg map[string]any) {
	if !mem9Enabled() {
		return
	}
	spec := mem9PluginSpec()
	plugins := configStringList(cfg["plugin"])
	for _, plugin := range plugins {
		if plugin == spec {
			cfg["plugin"] = plugins
			return
		}
	}
	cfg["plugin"] = append(plugins, spec)
}

func configStringList(value any) []any {
	switch v := value.(type) {
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if strings.TrimSpace(item) != "" {
				out = append(out, item)
			}
		}
		return out
	case string:
		if strings.TrimSpace(v) != "" {
			return []any{v}
		}
	}
	return nil
}

func ensureRunnerProviders(cfg map[string]any) {
	providers, _ := cfg["provider"].(map[string]any)
	if providers == nil {
		providers = make(map[string]any)
		cfg["provider"] = providers
	}

	addDeepSeekProvider(providers)
	addOpenCodeProvider(providers)
	addSyntheticProvider(providers)
}

func addDeepSeekProvider(providers map[string]any) {
	if apiKey := os.Getenv("DEEPSEEK_API_KEY"); apiKey != "" {
		if _, ok := providers["deepseek"]; !ok {
			providers["deepseek"] = map[string]any{
				"name":    "DeepSeek",
				"apiKey":  apiKey,
				"baseURL": "https://api.deepseek.com",
				"models": map[string]any{
					"deepseek-chat":     map[string]any{},
					"deepseek-v4-pro":   map[string]any{},
					"deepseek-v4-flash": map[string]any{},
				},
			}
		}
	}
}

func addOpenCodeProvider(providers map[string]any) {
	if apiKey := os.Getenv("OPENCODE_API_KEY"); apiKey != "" {
		if _, ok := providers["opencode"]; !ok {
			providers["opencode"] = map[string]any{
				"name":    "OpenCode Zen",
				"apiKey":  apiKey,
				"baseURL": "https://opencode.ai/zen/v1",
				"models": map[string]any{
					"deepseek-v4-flash-free": map[string]any{},
				},
			}
		}
	}
}

func addSyntheticProvider(providers map[string]any) {
	if _, ok := providers["synthetic"]; !ok {
		providers["synthetic"] = map[string]any{
			"name":   "Synthetic",
			"models": map[string]any{},
		}
	}
}

func ensureProvider(cfg map[string]any, providerID string) {
	providers, _ := cfg["provider"].(map[string]any)
	if providers == nil {
		providers = make(map[string]any)
		cfg["provider"] = providers
	}
	if _, ok := providers[providerID]; !ok {
		providers[providerID] = map[string]any{
			"name":   providerID,
			"models": map[string]any{},
		}
	}
}

func GenerateConfig(wsDir, socketPath, mcpBridgePath, chetterMCPURL, chetterMCPToken string, includeRunnerMCP, isLocal bool) error {
	wsConfigPath := wsDir + "/.opencode.json"
	data, err := os.ReadFile(wsConfigPath)
	configSource := wsConfigPath
	if err != nil {
		data, configSource = readOpenCodeConfig()
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		cfg = make(map[string]any)
	}
	slog.Info("opencode config source", "path", configSource, "bytes", len(data))
	ensureMem9Plugin(cfg)
	ensureRunnerProviders(cfg)

	if includeRunnerMCP {
		mcpServers, _ := cfg["mcp"].(map[string]any)
		if mcpServers == nil {
			mcpServers = make(map[string]any)
			cfg["mcp"] = mcpServers
		}
		mcpServers["runner-bridge"] = map[string]any{
			"type":    "local",
			"command": mcpBridgePath,
			"args":    []string{socketPath},
			"enabled": true,
		}
	}
	if chetterMCPURL != "" {
		mcpServers, _ := cfg["mcp"].(map[string]any)
		if mcpServers == nil {
			mcpServers = make(map[string]any)
			cfg["mcp"] = mcpServers
		}
		dfm := map[string]any{
			"type":    "remote",
			"url":     chetterMCPURL,
			"enabled": true,
		}
		if chetterMCPToken != "" {
			dfm["headers"] = map[string]string{
				"Authorization": "Bearer " + chetterMCPToken,
			}
		}
		mcpServers["chetter"] = dfm
		slog.Info("injected chetter MCP into opencode config", "url", chetterMCPURL)
	}

	perms := map[string]any{
		"bash": "allow",
		"read": "allow",
		"edit": "allow",
		"glob": "allow",
		"grep": "allow",
		"list": "allow",
	}
	perms["external_directory"] = map[string]string{
		"/tmp/*":  "allow",
		"/tmp/**": "allow",
	}
	cfg["permission"] = perms

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal opencode config: %w", err)
	}
	if err := os.WriteFile(wsConfigPath, out, 0644); err != nil {
		return fmt.Errorf("write opencode config: %w", err)
	}
	globalConfigDir := wsDir + "/.config/opencode"
	if err := os.MkdirAll(globalConfigDir, 0750); err != nil {
		return fmt.Errorf("create opencode config dir: %w", err)
	}
	globalConfigPath := globalConfigDir + "/config.json"
	if err := os.WriteFile(globalConfigPath, out, 0644); err != nil {
		return fmt.Errorf("write opencode global config: %w", err)
	}
	copyOpenCodeState(wsDir, isLocal)
	slog.Info("wrote opencode config", "path", wsConfigPath)
	slog.Info("wrote opencode global config", "path", globalConfigPath)
	return nil
}

func copyOpenCodeState(wsDir string, isLocal bool) {
	copyFirstExisting("opencode auth", wsDir+"/.local/share/opencode/auth.json", func(home string) []string {
		return []string{home + "/.local/share/opencode/auth.json"}
	})
	copyFirstExisting("opencode model state", wsDir+"/.local/state/opencode/model.json", func(home string) []string {
		return []string{home + "/.local/state/opencode/model.json"}
	})
	copyFirstExisting("opencode models cache", wsDir+"/.cache/opencode/models.json", func(home string) []string {
		return []string{home + "/.cache/opencode/models.json"}
	})

	if isLocal {
		copyOpenCodePluginState(wsDir)
	} else {
		for _, path := range []string{
			wsDir + "/.opencode/node_modules",
			wsDir + "/.opencode/package.json",
			wsDir + "/.config/opencode/node_modules",
			wsDir + "/.config/opencode/package.json",
		} {
			if err := os.RemoveAll(path); err != nil {
				slog.Warn("remove opencode plugin state warning", "path", path, "err", err)
			}
		}
		slog.Info("skipped workspace opencode plugin package state; harness image owns plugin dependencies")
	}

	rgDst := wsDir + "/.local/share/opencode/bin/rg"
	if _, err := os.Stat(rgDst); err != nil {
		for _, rgSrc := range []string{"/usr/bin/rg", "/usr/local/bin/rg", "/bin/rg"} {
			if data, err := os.ReadFile(rgSrc); err == nil {
				if err := os.MkdirAll(filepath.Dir(rgDst), 0750); err == nil {
					if err := os.WriteFile(rgDst, data, 0755); err == nil {
						slog.Info("pre-seeded ripgrep", "src", rgSrc, "dst", rgDst, "bytes", len(data))
						break
					}
				}
			}
		}
	}
}

func copyOpenCodePluginState(wsDir string) {
	for _, home := range candidateHomes() {
		nodeSrc := home + "/.opencode/node_modules"
		nodeDst := wsDir + "/.opencode/node_modules"
		if info, err := os.Stat(nodeSrc); err == nil && info.IsDir() {
			if err := copyDir(nodeSrc, nodeDst); err == nil {
				slog.Info("copied opencode plugins", "src", nodeSrc, "dst", nodeDst)
			} else {
				slog.Warn("copy opencode plugins warning", "err", err)
			}
			break
		}
	}

	actualVersion := ""
	for _, home := range candidateHomes() {
		pluginPkgPath := home + "/.opencode/node_modules/@opencode-ai/plugin/package.json"
		data, err := os.ReadFile(pluginPkgPath)
		if err == nil {
			var pkg map[string]any
			if json.Unmarshal(data, &pkg) == nil {
				if v, ok := pkg["version"].(string); ok && v != "" {
					actualVersion = v
					slog.Info("detected installed plugin version", "version", actualVersion)
					break
				}
			}
		}
	}
	if actualVersion == "" {
		return
	}
	pinPkg := map[string]any{
		"dependencies": map[string]string{
			"@opencode-ai/plugin": actualVersion,
			"zod":                 "4.1.8",
		},
	}
	pinData, _ := json.MarshalIndent(pinPkg, "", "  ")
	for _, dir := range []string{".opencode", ".config/opencode"} {
		pkgPath := filepath.Join(wsDir, dir, "package.json")
		if err := os.MkdirAll(filepath.Dir(pkgPath), 0750); err == nil {
			if err := os.WriteFile(pkgPath, pinData, 0644); err == nil {
				slog.Info("pinned package.json", "dir", dir, "version", actualVersion)
			}
		}
	}
}
