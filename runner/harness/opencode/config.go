package opencode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/flatout-works/chetter/runner/internal/task"
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

func ensureRunnerProvider(cfg map[string]any, req task.TaskRequest) {
	providers, _ := cfg["provider"].(map[string]any)
	if providers == nil {
		providers = make(map[string]any)
		cfg["provider"] = providers
	}
	providerID := strings.TrimSpace(req.ProviderID)
	modelID := strings.TrimSpace(req.ModelID)
	if providerID == "" || modelID == "" {
		return
	}
	entry, _ := providers[providerID].(map[string]any)
	if entry == nil {
		entry = map[string]any{
			"name":   firstNonEmpty(req.ProviderName, providerID),
			"models": make(map[string]any),
		}
		providers[providerID] = entry
	}
	if req.ProviderBaseURL != "" {
		entry["baseURL"] = req.ProviderBaseURL
	}
	if req.ProviderAPIKeyEnv != "" {
		if apiKey := os.Getenv(req.ProviderAPIKeyEnv); apiKey != "" {
			entry["apiKey"] = apiKey
		}
	}
	models, _ := entry["models"].(map[string]any)
	if models == nil {
		models = make(map[string]any)
		entry["models"] = models
	}
	models[modelID] = map[string]any{}
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
	return GenerateConfigForTask(wsDir, socketPath, mcpBridgePath, chetterMCPURL, chetterMCPToken, includeRunnerMCP, task.TaskRequest{}, isLocal)
}

func GenerateConfigWithEnv(wsDir, socketPath, mcpBridgePath, chetterMCPURL, chetterMCPToken string, includeRunnerMCP bool, taskEnv map[string]string, isLocal bool) error {
	return GenerateConfigForTask(wsDir, socketPath, mcpBridgePath, chetterMCPURL, chetterMCPToken, includeRunnerMCP, task.TaskRequest{Env: taskEnv}, isLocal)
}

func GenerateConfigForTask(wsDir, socketPath, mcpBridgePath, chetterMCPURL, chetterMCPToken string, includeRunnerMCP bool, req task.TaskRequest, isLocal bool) error {
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
	ensureRunnerProvider(cfg, req)

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

	if includeRunnerMCP {
		perms["mcp__runner-bridge__chetter_create_issue"] = "allow"
		perms["mcp__runner-bridge__chetter_issue_comment"] = "allow"
		perms["mcp__runner-bridge__chetter_create_pr"] = "allow"
		perms["mcp__runner-bridge__chetter_pr_review"] = "allow"
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
	writeAgentAndSkillDefinitions(wsDir, req)
	copyOpenCodeState(wsDir, isLocal)
	slog.Info("wrote opencode config", "path", wsConfigPath)
	slog.Info("wrote opencode global config", "path", globalConfigPath)
	return nil
}

func writeAgentAndSkillDefinitions(wsDir string, req task.TaskRequest) {
	if req.AgentDefinition != "" && req.Agent != "" {
		agentDir := wsDir + "/.config/opencode/agent"
		if err := os.MkdirAll(agentDir, 0750); err != nil {
			slog.Warn("create agent dir", "err", err)
		} else {
			path := agentDir + "/" + req.Agent + ".md"
			if err := os.WriteFile(path, []byte(req.AgentDefinition), 0644); err != nil {
				slog.Warn("write agent definition", "agent", req.Agent, "err", err)
			} else {
				slog.Info("injected agent definition", "agent", req.Agent, "path", path)
			}
		}
	}
	if len(req.SkillDefinitions) > 0 {
		skillsBase := wsDir + "/.config/opencode/skill"
		for name, tarBytes := range req.SkillDefinitions {
			skillDir := skillsBase + "/" + name
			if err := os.MkdirAll(skillDir, 0750); err != nil {
				slog.Warn("create skill dir", "skill", name, "err", err)
				continue
			}
			if err := untarSkill(tarBytes, skillDir); err != nil {
				slog.Warn("extract skill", "skill", name, "err", err)
			} else {
				slog.Info("injected skill", "skill", name, "dir", skillDir, "bytes", len(tarBytes))
			}
		}
	}
}

func untarSkill(data []byte, destDir string) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	destPrefix := filepath.Clean(destDir) + string(os.PathSeparator)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		path := filepath.Join(destDir, filepath.Clean(hdr.Name))
		if !strings.HasPrefix(path, destPrefix) {
			return fmt.Errorf("tar entry escapes dest dir: %s", hdr.Name)
		}
		if hdr.Size == 0 && hdr.Name == "" || strings.HasSuffix(hdr.Name, "/") {
			if err := os.MkdirAll(path, 0750); err != nil {
				return fmt.Errorf("mkdir %s: %w", path, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			return fmt.Errorf("mkdir parent %s: %w", path, err)
		}
		content, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil {
			return fmt.Errorf("read %s: %w", hdr.Name, err)
		}
		if err := os.WriteFile(path, content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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
