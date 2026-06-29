package opencode

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/flatout-works/chetter/runner/harness/mcpconfig"
	"github.com/flatout-works/chetter/runner/internal/safefs"
	"github.com/flatout-works/chetter/runner/internal/task"
)

const defaultMem9PluginSpec = "@mem9/opencode"
const managedMCPStatePath = ".opencode/.chetter-managed-mcp.json"

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

func clearRepoPlugins(cfg map[string]any) {
	delete(cfg, "plugin")
	delete(cfg, "plugins")
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

func currentManagedMCPServers(includeRunnerMCP, includeChetterMCP bool, profiles []task.MCPProfile) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 2+len(profiles))
	add := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	if includeRunnerMCP {
		add("runner-bridge")
	}
	if includeChetterMCP {
		add("chetter")
	}
	for _, profile := range profiles {
		add(profile.Name)
	}
	return out
}

func prepareMCPServers(wsDir string, cfg map[string]any, currentManaged []string) map[string]any {
	existing, _ := cfg["mcp"].(map[string]any)
	if existing == nil {
		existing = make(map[string]any)
	}
	previousManaged, _ := readManagedMCPState(wsDir)
	remove := make(map[string]struct{}, len(previousManaged)+len(currentManaged)+2)
	for _, name := range append(previousManaged, currentManaged...) {
		name = strings.TrimSpace(name)
		if name != "" {
			remove[name] = struct{}{}
		}
	}
	remove["runner-bridge"] = struct{}{}
	remove["chetter"] = struct{}{}

	mcpServers := make(map[string]any, len(existing)+len(currentManaged))
	for name, server := range existing {
		if _, ok := remove[name]; ok {
			continue
		}
		if openCodeRepoMCPServerIsUnsafe(server) {
			continue
		}
		mcpServers[name] = server
	}
	cfg["mcp"] = mcpServers
	return mcpServers
}

func readManagedMCPState(wsDir string) ([]string, bool) {
	data, err := safefs.ReadFile(wsDir, managedMCPStatePath)
	if err != nil {
		return nil, false
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return nil, false
	}
	return nonEmptyUniqueStrings(names), true
}

func writeManagedMCPState(wsDir string, names []string) error {
	data, err := json.MarshalIndent(nonEmptyUniqueStrings(names), "", "  ")
	if err != nil {
		return fmt.Errorf("marshal opencode managed MCP state: %w", err)
	}
	if err := safefs.WriteFile(wsDir, managedMCPStatePath, data, 0644); err != nil {
		return fmt.Errorf("write opencode managed MCP state: %w", err)
	}
	return nil
}

func nonEmptyUniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func openCodeRepoMCPServerIsUnsafe(server any) bool {
	serverMap, ok := server.(map[string]any)
	if !ok {
		return true
	}
	for _, key := range []string{"command", "args", "env", "cwd"} {
		if openCodeMCPFieldPresent(serverMap[key]) {
			return true
		}
	}
	if openCodeMCPStringIsLocalProcess(serverMap["type"]) || openCodeMCPStringIsLocalProcess(serverMap["transport"]) {
		return true
	}
	if strings.TrimSpace(stringValue(serverMap["url"])) == "" {
		return true
	}
	profile := task.MCPProfile{
		URL:     stringValue(serverMap["url"]),
		Headers: headerStringMap(serverMap["headers"]),
	}
	return mcpconfig.ProfileCarriesCredentials(profile)
}

func openCodeMCPStringIsLocalProcess(value any) bool {
	switch strings.ToLower(strings.TrimSpace(stringValue(value))) {
	case "local", "stdio", "command":
		return true
	default:
		return false
	}
}

func openCodeMCPFieldPresent(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	case []any:
		return len(v) > 0
	case []string:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	case map[string]string:
		return len(v) > 0
	default:
		return true
	}
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func headerStringMap(value any) map[string]string {
	switch headers := value.(type) {
	case map[string]string:
		out := make(map[string]string, len(headers))
		for key, value := range headers {
			out[key] = value
		}
		return out
	case map[string]any:
		out := make(map[string]string, len(headers))
		for key, value := range headers {
			if s, ok := value.(string); ok {
				out[key] = s
			}
		}
		return out
	default:
		return nil
	}
}

func GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, includeRunnerMCP, isLocal bool) error {
	return GenerateConfigForTaskWithRunnerToken(wsDir, runnerMCPURL, "", chetterMCPURL, chetterMCPToken, includeRunnerMCP, task.TaskRequest{}, isLocal)
}

func GenerateConfigWithEnv(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, includeRunnerMCP bool, taskEnv map[string]string, isLocal bool) error {
	return GenerateConfigForTaskWithRunnerToken(wsDir, runnerMCPURL, "", chetterMCPURL, chetterMCPToken, includeRunnerMCP, task.TaskRequest{Env: taskEnv}, isLocal)
}

func GenerateConfigForTask(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, includeRunnerMCP bool, req task.TaskRequest, isLocal bool) error {
	return GenerateConfigForTaskWithRunnerToken(wsDir, runnerMCPURL, "", chetterMCPURL, chetterMCPToken, includeRunnerMCP, req, isLocal)
}

func GenerateConfigForTaskWithRunnerToken(wsDir, runnerMCPURL, runnerMCPToken, chetterMCPURL, chetterMCPToken string, includeRunnerMCP bool, req task.TaskRequest, isLocal bool) error {
	const wsConfigRelPath = ".opencode.json"
	wsConfigPath := filepath.Join(wsDir, wsConfigRelPath)
	data, err := safefs.ReadFile(wsDir, wsConfigRelPath)
	configSource := wsConfigPath
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read opencode workspace config: %w", err)
		}
		data, configSource = readOpenCodeConfig()
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		cfg = make(map[string]any)
	}
	slog.Info("opencode config source", "path", configSource, "bytes", len(data))
	clearRepoPlugins(cfg)
	ensureMem9Plugin(cfg)
	ensureRunnerProvider(cfg, req)
	currentManagedMCP := currentManagedMCPServers(includeRunnerMCP && runnerMCPURL != "", chetterMCPURL != "", req.MCPProfiles)
	mcpServers := prepareMCPServers(wsDir, cfg, currentManagedMCP)

	if includeRunnerMCP && runnerMCPURL != "" {
		bridge := map[string]any{
			"type":    "remote",
			"url":     runnerMCPURL,
			"enabled": true,
		}
		if runnerMCPToken != "" {
			bridge["headers"] = map[string]string{
				"Authorization": "Bearer " + runnerMCPToken,
			}
		}
		mcpServers["runner-bridge"] = bridge
	}
	if chetterMCPURL != "" {
		if chetterMCPToken != "" {
			if err := mcpconfig.RejectToolAllowlistsForURL(req.MCPProfiles, chetterMCPURL, "OpenCode Chetter MCP config"); err != nil {
				return err
			}
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
	if len(req.MCPProfiles) > 0 {
		if err := mcpconfig.AddOpenCodeServers(mcpServers, req.MCPProfiles); err != nil {
			return err
		}
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
		mcpconfig.AddOpenCodeToolPermission(perms, "runner-bridge", "chetter_create_issue", "allow")
		mcpconfig.AddOpenCodeToolPermission(perms, "runner-bridge", "chetter_issue_comment", "allow")
		mcpconfig.AddOpenCodeToolPermission(perms, "runner-bridge", "chetter_create_pr", "allow")
		mcpconfig.AddOpenCodeToolPermission(perms, "runner-bridge", "chetter_pr_review", "allow")
	}
	mcpconfig.AddOpenCodePermissions(perms, req.MCPProfiles)

	cfg["permission"] = perms

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal opencode config: %w", err)
	}
	if err := safefs.WriteFile(wsDir, wsConfigRelPath, out, 0644); err != nil {
		return fmt.Errorf("write opencode config: %w", err)
	}
	if err := writeManagedMCPState(wsDir, currentManagedMCP); err != nil {
		return err
	}
	globalConfigRelPath := ".config/opencode/config.json"
	globalConfigPath := filepath.Join(wsDir, globalConfigRelPath)
	if err := safefs.WriteFile(wsDir, globalConfigRelPath, out, 0644); err != nil {
		return fmt.Errorf("write opencode global config: %w", err)
	}
	if err := writeAgentAndSkillDefinitions(wsDir, req); err != nil {
		return err
	}
	if err := copyOpenCodeState(wsDir, isLocal); err != nil {
		return err
	}
	slog.Info("wrote opencode config", "path", wsConfigPath)
	slog.Info("wrote opencode global config", "path", globalConfigPath)
	return nil
}

func writeAgentAndSkillDefinitions(wsDir string, req task.TaskRequest) error {
	if req.AgentDefinition != "" && req.Agent != "" {
		agentRelPath := filepath.Join(".config", "opencode", "agent", req.Agent+".md")
		if err := safefs.WriteFile(wsDir, agentRelPath, []byte(req.AgentDefinition), 0644); err != nil {
			return fmt.Errorf("write agent definition %q: %w", req.Agent, err)
		}
		slog.Info("injected agent definition", "agent", req.Agent, "path", filepath.Join(wsDir, agentRelPath))
	}
	if len(req.SkillDefinitions) > 0 {
		for name, tarBytes := range req.SkillDefinitions {
			skillRelDir := filepath.Join(".config", "opencode", "skill", name)
			if err := safefs.EnsureDir(wsDir, skillRelDir, 0750); err != nil {
				return fmt.Errorf("create skill dir %q: %w", name, err)
			}
			if err := untarSkill(tarBytes, wsDir, skillRelDir); err != nil {
				return fmt.Errorf("extract skill %q: %w", name, err)
			}
			slog.Info("injected skill", "skill", name, "dir", filepath.Join(wsDir, skillRelDir), "bytes", len(tarBytes))
		}
	}
	return nil
}

func untarSkill(data []byte, wsDir, destRelDir string) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	destRelDir = filepath.Clean(destRelDir)
	destPrefix := destRelDir + string(os.PathSeparator)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		entryRelPath := filepath.Join(destRelDir, filepath.Clean(hdr.Name))
		if entryRelPath != destRelDir && !strings.HasPrefix(entryRelPath, destPrefix) {
			return fmt.Errorf("tar entry escapes dest dir: %s", hdr.Name)
		}
		if hdr.Name == "" {
			return fmt.Errorf("tar entry has empty name")
		}
		if hdr.Typeflag == tar.TypeDir || strings.HasSuffix(hdr.Name, "/") {
			if err := safefs.EnsureDir(wsDir, entryRelPath, 0750); err != nil {
				return fmt.Errorf("mkdir %s: %w", entryRelPath, err)
			}
			continue
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA && hdr.Typeflag != 0 {
			return fmt.Errorf("unsupported tar entry type %d: %s", hdr.Typeflag, hdr.Name)
		}
		content, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
		if err != nil {
			return fmt.Errorf("read %s: %w", hdr.Name, err)
		}
		if err := safefs.WriteFile(wsDir, entryRelPath, content, 0644); err != nil {
			return fmt.Errorf("write %s: %w", entryRelPath, err)
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

func copyOpenCodeState(wsDir string, isLocal bool) error {
	copyFirstExisting("opencode auth", wsDir, ".local/share/opencode/auth.json", func(home string) []string {
		return []string{home + "/.local/share/opencode/auth.json"}
	})
	copyFirstExisting("opencode model state", wsDir, ".local/state/opencode/model.json", func(home string) []string {
		return []string{home + "/.local/state/opencode/model.json"}
	})
	copyFirstExisting("opencode models cache", wsDir, ".cache/opencode/models.json", func(home string) []string {
		return []string{home + "/.cache/opencode/models.json"}
	})

	if err := removeOpenCodePluginState(wsDir); err != nil {
		return err
	}

	if isLocal {
		copyOpenCodePluginState(wsDir)
	} else {
		slog.Info("skipped workspace opencode plugin package state; harness image owns plugin dependencies")
	}

	const rgRelDst = ".local/share/opencode/bin/rg"
	if _, err := safefs.ReadFile(wsDir, rgRelDst); err != nil {
		for _, rgSrc := range []string{"/usr/bin/rg", "/usr/local/bin/rg", "/bin/rg"} {
			if data, err := os.ReadFile(rgSrc); err == nil {
				if err := safefs.WriteFile(wsDir, rgRelDst, data, 0755); err == nil {
					slog.Info("pre-seeded ripgrep", "src", rgSrc, "dst", filepath.Join(wsDir, rgRelDst), "bytes", len(data))
					break
				}
			}
		}
	}
	return nil
}

func removeOpenCodePluginState(wsDir string) error {
	for _, relPath := range []string{
		".opencode/node_modules",
		".opencode/package.json",
		".opencode/plugin",
		".opencode/plugins",
		".config/opencode/node_modules",
		".config/opencode/package.json",
		".config/opencode/plugin",
		".config/opencode/plugins",
	} {
		if err := safefs.RemoveAll(wsDir, relPath); err != nil {
			return fmt.Errorf("remove opencode plugin state %s: %w", relPath, err)
		}
	}
	return nil
}

func copyOpenCodePluginState(wsDir string) {
	for _, home := range candidateHomes() {
		nodeSrc := home + "/.opencode/node_modules"
		nodeRelDst := ".opencode/node_modules"
		if info, err := os.Stat(nodeSrc); err == nil && info.IsDir() {
			if err := copyDir(nodeSrc, wsDir, nodeRelDst); err == nil {
				slog.Info("copied opencode plugins", "src", nodeSrc, "dst", filepath.Join(wsDir, nodeRelDst))
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
		pkgRelPath := filepath.Join(dir, "package.json")
		if err := safefs.WriteFile(wsDir, pkgRelPath, pinData, 0644); err == nil {
			slog.Info("pinned package.json", "dir", dir, "version", actualVersion)
		}
	}
}
