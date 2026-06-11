package controller

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/flatout-works/devfleet/runner/internal/containerd"
	"github.com/flatout-works/devfleet/runner/internal/mcp"
	"github.com/flatout-works/devfleet/runner/internal/network"
	"github.com/flatout-works/devfleet/runner/internal/task"
	"github.com/flatout-works/devfleet/runner/internal/tools"
	nats "github.com/nats-io/nats.go"
)

const defaultMem9PluginSpec = "@mem9/opencode"

// opencodeEventLineMax is the maximum size of a single line read from the
// OpenCode event stream. OpenCode emits JSON events that can include large
// payloads (PR diffs, file contents, tool outputs) on a single line, so the
// default 64KB bufio.Scanner buffer is too small.
const opencodeEventLineMax = 4 * 1024 * 1024 // 4 MiB

// runTask is the main task lifecycle: workspace creation, optional git clone,
// MCP server setup, network bridge creation, and agent spawn (local or Kata).
// Results are published via publishStatus.
func (r *Runner) runTask(req task.TaskRequest, msg *nats.Msg) {
	defer func() { <-r.sem }()
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("runner panic", "taskID", req.TaskID, "panic", rec)
			r.publishStatusForRequest(req, "error", fmt.Sprintf("runner panic: %v", rec), nil)
			panic(rec)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(req.TimeoutSec)*time.Second)
	defer cancel()

	session := &task.TaskSession{
		TaskID:    req.TaskID,
		Request:   req,
		Cancel:    cancel,
		StartedAt: time.Now(),
	}
	r.mu.Lock()
	r.tasks[req.TaskID] = session
	r.totalStarted++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.tasks, req.TaskID)
		r.mu.Unlock()
	}()

	r.publishStatusForRequest(req, "running", "Preparing workspace...", nil)
	r.publishActivityEvent("agent", "Task Started", fmt.Sprintf("Task %s started", req.TaskID), "running", "", 0)

	wsDir, err := r.wsManager.Create(req.TaskID)
	if err != nil {
		r.publishStatusForRequest(req, "error", err.Error(), nil)
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Workspace creation failed: %v", err), "failed", err.Error(), time.Since(session.StartedAt).Milliseconds())
		return
	}
	session.WorkspaceDir = wsDir

	defer func() {
		if err := r.wsManager.Destroy(req.TaskID); err != nil {
			slog.Warn("cleanup error", "taskID", req.TaskID, "err", err)
		}
	}()

	gitURL := req.GitURL
	if req.GitURL != "" {
		slog.Info("cloning", "taskID", req.TaskID, "url", req.GitURL)
		if err := os.RemoveAll(wsDir); err != nil {
			slog.Warn("removing stale workspace", "taskID", req.TaskID, "err", err)
		}
		if err := os.MkdirAll(wsDir, 0750); err != nil {
			r.publishStatusForRequest(req, "error", err.Error(), nil)
			return
		}
		// Inject PAT into HTTPS URL for non-interactive authentication.
		// GitHub does not support GIT_USERNAME/GIT_PASSWORD env vars.
		if r.cfg.Git.PAT != "" && strings.HasPrefix(req.GitURL, "https://") {
			gitURL = injectPATIntoURL(req.GitURL, r.cfg.Git.PAT)
		}
		cloneCmd := exec.CommandContext(ctx, "git", "clone")
		if req.GitRef != "" {
			cloneCmd.Args = append(cloneCmd.Args, "-b", req.GitRef)
		}
		cloneCmd.Args = append(cloneCmd.Args, gitURL, ".")
		cloneCmd.Dir = wsDir
		if r.cfg.Git.SSHKeyPath != "" {
			cloneCmd.Env = append(os.Environ(), "GIT_SSH_COMMAND=ssh -i "+r.cfg.Git.SSHKeyPath+" -o StrictHostKeyChecking=no")
		}
		if out, err := cloneCmd.CombinedOutput(); err != nil {
			slog.Error("clone error", "taskID", req.TaskID, "err", err, "output", string(out))
			r.publishStatusForRequest(req, "error", fmt.Sprintf("git clone: %v\n%s", err, string(out)), nil)
			r.publishActivityEvent("repo", "Git Clone Failed", fmt.Sprintf("Failed to clone %s", req.GitURL), "failed", fmt.Sprintf("%v\n%s", err, string(out)), time.Since(session.StartedAt).Milliseconds())
			return
		}
	}

	socketPath := r.wsManager.SocketPath(req.TaskID)

	// Generate OpenCode config (always — builder writes providers, runner adds MCP).
	if err := r.generateOpenCodeConfig(wsDir, socketPath, true); err != nil {
		slog.Warn("opencode config warning", "taskID", req.TaskID, "err", err)
	}

	// Create MCP server with workspace, git, deploy, and fetch tools.
	mcpServer, err := mcp.NewServer(socketPath)
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("mcp server: %v", err), nil)
		return
	}
	defer mcpServer.Close()

	ws := tools.NewWorkspace(wsDir)
	git := tools.NewGit(wsDir, r.cfg.Git.SSHKeyPath, r.cfg.Git.PAT)
	nt := &tools.NatsTool{Client: r.natsClient}
	deploy := tools.NewDeploy(
		wsDir,
		tools.DeployProvider(r.cfg.Deploy.Provider),
		req.TaskID,
		r.cfg.Deploy.Registry,
		r.cfg.Deploy.DevfleetURL,
	)

	mcpServer.RegisterTool("workspace_read_file", ws.ReadFile)
	mcpServer.RegisterTool("workspace_write_file", ws.WriteFile)
	mcpServer.RegisterTool("workspace_list_directory", ws.ListDirectory)
	mcpServer.RegisterTool("workspace_bash", ws.Bash)
	mcpServer.RegisterTool("git_status", git.Status)
	mcpServer.RegisterTool("git_pull", git.Pull)
	mcpServer.RegisterTool("git_push", git.Push)
	mcpServer.RegisterTool("git_commit", git.Commit)
	mcpServer.RegisterTool("nats_publish", nt.Publish)
	mcpServer.RegisterTool("nats_request", nt.Request)
	mcpServer.RegisterTool("fetch_url", tools.Fetch)
	mcpServer.RegisterTool("deploy_build", deploy.Build)
	mcpServer.RegisterTool("deploy_push", deploy.Push)
	mcpServer.RegisterTool("deploy_run", deploy.Run)
	mcpServer.RegisterTool("deploy_status", deploy.Status)
	mcpServer.RegisterTool("deploy_stop", deploy.Stop)
	mcpServer.RegisterTool("deploy_logs", deploy.Logs)
	mcpServer.RegisterTool("deploy_list", deploy.ListContainers)
	mcpServer.RegisterTool("deploy_versions", deploy.ListVersions)
	mcpServer.RegisterTool("deploy_rollback", deploy.Rollback)

	go mcpServer.Serve(ctx)
	slog.Info("MCP server started", "taskID", req.TaskID, "socket", socketPath)

	// Network isolation is Kata-only.
	var taskNet *network.TaskNetwork
	if r.executionMode() == "kata" {
		taskNet, err = r.bridgeMgr.Setup(ctx, req.TaskID)
		if err != nil {
			slog.Error("bridge setup error", "taskID", req.TaskID, "err", err)
			r.publishStatusForRequest(req, "error", fmt.Sprintf("network isolation setup: %v", err), nil)
			return
		}
		slog.Info("network bridge ready", "taskID", req.TaskID, "bridge", taskNet.Bridge)
		defer func() {
			if err := r.bridgeMgr.Teardown(ctx, taskNet); err != nil {
				slog.Error("bridge teardown error", "taskID", req.TaskID, "err", err)
			}
		}()
	}

	if req.AgentImage == "" {
		r.publishStatusForRequest(req, "error", "agent_image is required", nil)
		return
	}

	switch r.executionMode() {
	case "local":
		r.runLocalAgent(ctx, session, req, socketPath)
	case "docker":
		r.runDockerAgent(ctx, session, req, socketPath)
	default:
		r.runKataAgent(ctx, session, req, socketPath, taskNet)
	}
}

// generateOpenCodeConfig writes a .opencode.json into the workspace.
// It reads the workspace config first (builder writes LLM providers there),
// then falls back to the host config. MCP server config is always added.
func (r *Runner) generateOpenCodeConfig(wsDir, socketPath string, includeRunnerMCP bool) error {
	// Try workspace config first — the builder writes LLM providers and skills here.
	wsConfigPath := wsDir + "/.opencode.json"
	data, err := os.ReadFile(wsConfigPath)
	configSource := wsConfigPath
	if err != nil {
		// Fall back to host config.
		data, configSource = readOpenCodeConfig()
	}
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		cfg = make(map[string]any)
	}
	slog.Info("opencode config source", "path", configSource, "bytes", len(data))
	ensureMem9Plugin(cfg)
	ensureRunnerProviders(cfg)

	// Always add the runner MCP bridge when requested.
	if includeRunnerMCP {
		mcpServers, _ := cfg["mcp"].(map[string]any)
		if mcpServers == nil {
			mcpServers = make(map[string]any)
			cfg["mcp"] = mcpServers
		}
		// In Docker mode, the mcp-bridge is inside the container at /usr/local/bin/mcp-bridge.
		// In local mode, it's next to the runner binary on the host.
		bridgeCmd := r.mcpBridgePath()
		if r.executionMode() == "docker" {
			bridgeCmd = "/usr/local/bin/mcp-bridge"
		}
		mcpServers["runner-bridge"] = map[string]any{
			"type":    "local",
			"command": bridgeCmd,
			"args":    []string{socketPath},
			"enabled": true,
		}
	}
	// Always inject devfleet MCP if configured — available in both local and Kata modes.
	if r.cfg.DevfleetMCP.URL != "" {
		mcpServers, _ := cfg["mcp"].(map[string]any)
		if mcpServers == nil {
			mcpServers = make(map[string]any)
			cfg["mcp"] = mcpServers
		}
		dfm := map[string]any{
			"type":    "remote",
			"url":     r.cfg.DevfleetMCP.URL,
			"enabled": true,
		}
		if r.cfg.DevfleetMCP.AuthToken != "" {
			dfm["headers"] = map[string]string{
				"Authorization": "Bearer " + r.cfg.DevfleetMCP.AuthToken,
			}
		}
		mcpServers["devfleet"] = dfm
		slog.Info("injected devfleet MCP into opencode config", "url", r.cfg.DevfleetMCP.URL)
	}

	// Pre-approve permissions so agents don't get stuck on interactive prompts.
	// Task containers are already isolated by Kata VM + network sandbox, so
	// blanket-allow workspace operations and /tmp/ access is safe.
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
	r.copyOpenCodeState(wsDir)
	slog.Info("wrote opencode config", "path", wsConfigPath)
	slog.Info("wrote opencode global config", "path", globalConfigPath)
	return nil
}

// mcpBridgePath returns the absolute path to the mcp-bridge binary.
// It looks next to the runner binary first, then falls back to PATH.
func (r *Runner) mcpBridgePath() string {
	exe, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "mcp-bridge")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "mcp-bridge"
}

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

func appendRunnerOwnedEnv(env []string) []string {
	for _, key := range runnerOwnedEnvKeys() {
		if value := os.Getenv(key); value != "" {
			env = append(env, key+"="+value)
		}
	}
	return env
}

func addRunnerOwnedEnv(env map[string]string) {
	for _, key := range runnerOwnedEnvKeys() {
		if value := os.Getenv(key); value != "" {
			env[key] = value
		}
	}
}

func runnerOwnedEnvKeys() []string {
	return []string{"MEM9_API_KEY", "MEM9_API_URL", "MEM9_DEBUG", "MEM9_HOME", "OPENAI_API_KEY", "DEEPSEEK_API_KEY", "OPENCODE_API_KEY"}
}

func isRunnerOwnedEnv(key string) bool {
	switch key {
	case "MEM9_API_KEY", "MEM9_API_URL", "MEM9_DEBUG", "MEM9_HOME", "OPENAI_API_KEY", "DEEPSEEK_API_KEY", "OPENCODE_API_KEY":
		return true
	default:
		return false
	}
}

// ensureRunnerProviders configures LLM providers from the runner's environment
// variables so OpenCode can use them. At minimum, synthetic is configured as a
// fallback so OpenCode session creation does not fail.
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

func (r *Runner) copyOpenCodeState(wsDir string) {
	copyFirstExisting("opencode auth", wsDir+"/.local/share/opencode/auth.json", func(home string) []string {
		return []string{home + "/.local/share/opencode/auth.json"}
	})
	copyFirstExisting("opencode model state", wsDir+"/.local/state/opencode/model.json", func(home string) []string {
		return []string{home + "/.local/state/opencode/model.json"}
	})
	copyFirstExisting("opencode models cache", wsDir+"/.cache/opencode/models.json", func(home string) []string {
		return []string{home + "/.cache/opencode/models.json"}
	})

	if r.executionMode() == "local" {
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

func (r *Runner) resolveCommand(req task.TaskRequest) []string {
	if len(req.Command) > 0 {
		return req.Command
	}
	if req.Prompt != "" {
		model := modelFlag(req)
		cmd := []string{
			"opencode", "run",
		}
		if !mem9Enabled() {
			cmd = append(cmd, "--pure")
		}
		cmd = append(cmd,
			"--port", "0",
			"--dir", "/workspace",
			"--print-logs",
			"--log-level", "DEBUG",
		)
		if model != "" {
			cmd = append(cmd, "-m", model)
		}
		if req.VariantID != "" {
			cmd = append(cmd, "--variant", req.VariantID)
		}
		if req.Agent != "" {
			cmd = append(cmd, "--agent", req.Agent)
		}
		cmd = append(cmd, promptWithSkillHints(req.Prompt, req.Skills), "--format", "json", "--dangerously-skip-permissions")
		return cmd
	}
	return nil
}

func modelFlag(req task.TaskRequest) string {
	if req.ProviderID != "" && req.ModelID != "" {
		return req.ProviderID + "/" + req.ModelID
	}
	if req.ModelID != "" {
		return req.ModelID
	}
	provider := req.Env["LLM_PROVIDER"]
	model := req.Env["LLM_MODEL_CODER"]
	if provider != "" && model != "" {
		return provider + "/" + model
	}
	if model != "" {
		return model
	}
	return ""
}

func promptWithSkillHints(prompt string, skills []string) string {
	if len(skills) == 0 {
		return prompt
	}
	return "Requested OpenCode skills: " + strings.Join(skills, ", ") + ". Use these skills when applicable.\n\n" + prompt
}

func promptModel(req task.TaskRequest, defaultProvider, defaultModel string) (string, string) {
	providerID := req.ProviderID
	modelID := req.ModelID
	if providerID == "" && strings.Contains(modelID, "/") {
		parts := strings.SplitN(modelID, "/", 2)
		providerID = parts[0]
		modelID = parts[1]
	}
	if providerID == "" {
		providerID = envValue(req.Env, "LLM_PROVIDER", defaultProvider)
	}
	if modelID == "" {
		modelID = envValue(req.Env, "LLM_MODEL_CODER", defaultModel)
	}
	return providerID, modelID
}

func promptVariant(req task.TaskRequest) string {
	if req.VariantID != "" {
		return req.VariantID
	}
	return envValue(req.Env, "LLM_VARIANT", "")
}

func shellQuoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuoteArg(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuoteArg(arg string) string {
	return "'" + strings.ReplaceAll(arg, "'", "'\"'\"'") + "'"
}

// injectPATIntoURL embeds a personal access token into an HTTPS git URL
// so that non-interactive git clone can authenticate. The token is placed
// as the userinfo component: https://<token>:x-oauth-basic@host/path.
func injectPATIntoURL(raw, pat string) string {
	if !strings.HasPrefix(raw, "https://") {
		return raw
	}
	rest := strings.TrimPrefix(raw, "https://")
	return "https://" + pat + ":x-oauth-basic@" + rest
}

// runKataAgent pulls the agent image, sets up Kata mounts and environment,
// and runs the agent inside a Kata Containers micro-VM with network isolation.
func (r *Runner) runKataAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, socketPath string, taskNet *network.TaskNetwork) {
	if taskNet == nil {
		r.publishStatusForRequest(req, "error", "network isolation is required in Kata mode", nil)
		return
	}

	slog.Info("pulling image", "taskID", req.TaskID, "image", req.AgentImage)
	if err := r.containerd.Pull(ctx, req.AgentImage); err != nil {
		slog.Warn("pull warning", "taskID", req.TaskID, "err", err)
	}

	env := map[string]string{
		"TASK_ID":         req.TaskID,
		"WORKSPACE":       "/workspace",
		"MCP_SOCKET_PATH": "/run/mcp/agent.sock",
		"HOME":            "/opt/opencode",
		"XDG_CONFIG_HOME": "/opt/opencode/.config",
		"XDG_DATA_HOME":   "/workspace/.local/share",
		"XDG_STATE_HOME":  "/workspace/.local/state",
		"XDG_CACHE_HOME":  "/workspace/.cache",
		"PATH":            "/workspace/.local/share/opencode/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	if taskNet != nil {
		proxyHost := taskNet.GatewayIP + r.cfg.Proxy.ListenAddr
		env["DEVFLEET_PROXY"] = proxyHost
		env["HTTP_PROXY"] = "http://" + proxyHost
		env["HTTPS_PROXY"] = "http://" + proxyHost
		env["NO_PROXY"] = "localhost,127.0.0.1,.local"
	}
	for k, v := range req.Env {
		if isRunnerOwnedEnv(k) {
			continue
		}
		env[k] = v
	}
	addRunnerOwnedEnv(env)
	resolvConfPath := session.WorkspaceDir + "/.devfleet-resolv.conf"
	if err := os.WriteFile(resolvConfPath, []byte("nameserver "+taskNet.GatewayIP+"\noptions timeout:2 attempts:2\n"), 0644); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("write task resolv.conf: %v", err), nil)
		return
	}

	mounts := []containerd.Mount{
		{
			Type:        "bind",
			Source:      session.WorkspaceDir,
			Destination: "/workspace",
			Options:     []string{"rbind", "rw"},
		},
		{
			Type:        "bind",
			Source:      resolvConfPath,
			Destination: "/etc/resolv.conf",
			Options:     []string{"rbind", "ro"},
		},
		{
			Type:        "bind",
			Source:      session.WorkspaceDir + "/.config/opencode/config.json",
			Destination: "/opt/opencode/.config/opencode/config.json",
			Options:     []string{"rbind", "ro"},
		},
	}

	cmd := r.resolveCommand(req)
	if os.Getenv("DEVFLEET_KATA_PREFLIGHT") == "1" && len(req.Command) == 0 && req.Prompt != "" {
		mounts = append(mounts,
			containerd.Mount{Type: "bind", Source: "/usr/bin/strace", Destination: "/usr/bin/strace", Options: []string{"rbind", "ro"}},
			containerd.Mount{Type: "bind", Source: "/lib/x86_64-linux-gnu", Destination: "/lib/x86_64-linux-gnu", Options: []string{"rbind", "ro"}},
			containerd.Mount{Type: "bind", Source: "/lib64", Destination: "/lib64", Options: []string{"rbind", "ro"}},
			containerd.Mount{Type: "bind", Source: "/tmp", Destination: "/host-tmp", Options: []string{"rbind", "rw"}},
		)
		cmd = kataPreflightCommand(cmd)
	} else if len(req.Command) == 0 && req.Prompt != "" {
		cmd = kataRunCommand(cmd)
	}
	slog.Info("starting Kata container", "taskID", req.TaskID, "command", cmd)
	out, err := r.containerd.RunKata(ctx, req.TaskID, req.AgentImage, mounts, env, taskNet.NetNSPath, cmd)
	if err != nil {
		slog.Error("kata run error", "taskID", req.TaskID, "err", err)
		r.publishStatusForRequest(req, "error", fmt.Sprintf("kata run: %v\n%s", err, out), nil)
		return
	}

	slog.Info("kata container exited", "taskID", req.TaskID)
	summary := out
	if len(req.Command) == 0 && req.Prompt != "" {
		summary = summarizeOpenCodeJSONL(out)
	}
	r.publishStatusForRequest(req, "done", truncateSummary(summary), nil)
	r.publishActivityEvent("agent", "Task Completed", fmt.Sprintf("Task %s completed", req.TaskID), "success", truncateSummary(summary), time.Since(session.StartedAt).Milliseconds())
}

func kataRunCommand(cmd []string) []string {
	return []string{"sh", "-c", "cd /tmp && exec " + shellQuoteArgs(cmd) + " < /dev/null"}
}

func opencodeServeArgs(port int) []string {
	args := []string{"serve"}
	if !mem9Enabled() {
		args = append(args, "--pure")
	}
	return append(args, "--port", strconv.Itoa(port))
}

func summarizeOpenCodeJSONL(out string) string {
	type event struct {
		Type string `json:"type"`
		Part struct {
			Text string `json:"text"`
		} `json:"part"`
	}

	var texts []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var evt event
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}
		if evt.Type == "text" && strings.TrimSpace(evt.Part.Text) != "" {
			texts = append(texts, evt.Part.Text)
		}
	}
	if len(texts) > 0 {
		return strings.Join(texts, "\n")
	}
	return out
}

// runLocalAgent spawns OpenCode as a local process (no VM isolation),
// creates a session, and sends the prompt.
func (r *Runner) runLocalAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, socketPath string) {
	env := os.Environ()
	for k, v := range req.Env {
		if isRunnerOwnedEnv(k) {
			continue
		}
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	env = appendRunnerOwnedEnv(env)
	env = append(env,
		"GIT_AUTHOR_NAME=Devfleet Runner",
		"GIT_AUTHOR_EMAIL=devfleet@devfleet.works",
		"GIT_COMMITTER_NAME=Devfleet Runner",
		"GIT_COMMITTER_EMAIL=devfleet@devfleet.works",
	)

	secret := generatePassword()
	env = append(env,
		"TASK_ID="+req.TaskID,
		"WORKSPACE="+session.WorkspaceDir,
		"MCP_SOCKET_PATH="+socketPath,
		"OPENCODE_SERVER_PASSWORD="+secret,
		// Isolate OpenCode from the host's ~/.opencode/ so it only sees
		// workspace skills (not the developer's local skills/plugins).
		// Auth and model state are already copied into the workspace by copyOpenCodeState.
		"HOME="+session.WorkspaceDir,
	)

	if req.Prompt == "" {
		r.publishStatusForRequest(req, "error", "no prompt provided", nil)
		return
	}

	// OpenCode config already generated by runTask with includeRunnerMCP=true.
	env = append(env, "OPENCODE_CONFIG="+filepath.Join(session.WorkspaceDir, ".opencode.json"))

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("allocate port: %v", err), nil)
		return
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	serveCmd := exec.CommandContext(ctx, "opencode", opencodeServeArgs(port)...)
	serveCmd.Dir = session.WorkspaceDir
	serveCmd.Env = env
	stdout, err := serveCmd.StdoutPipe()
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("opencode stdout pipe: %v", err), nil)
		return
	}
	stderr, err := serveCmd.StderrPipe()
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("opencode stderr pipe: %v", err), nil)
		return
	}

	if err := serveCmd.Start(); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("start opencode serve: %v", err), nil)
		return
	}
	go r.pipeOpenCodeOutput(req.TaskID, "stdout", stdout)
	go r.pipeOpenCodeOutput(req.TaskID, "stderr", stderr)

	defer func() {
		if serveCmd.Process != nil {
			serveCmd.Process.Kill()
			serveCmd.Wait()
		}
	}()

	if err := waitForServeReady(baseURL, secret, serveReadyTimeout); err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("opencode serve not ready: %v", err), nil)
		return
	}
	slog.Info("opencode serve", "taskID", req.TaskID, "url", baseURL)

	sid, err := createOpenCodeSession(baseURL, secret)
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("create session: %v", err), nil)
		return
	}
	slog.Info("session", "taskID", req.TaskID, "sessionID", sid)

	eventsCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	go r.watchOpenCodeEvents(eventsCtx, req.TaskID, baseURL, secret)

	r.publishStatusForRequest(req, "running", "Sending prompt to agent...", nil)
	summary, err := sendPromptAndWait(baseURL, sid, secret, req, taskPromptTimeout(req.TimeoutSec))
	if err != nil {
		r.publishStatusWithMetadata(req, "error", fmt.Sprintf("prompt failed: %v", err), nil, sid)
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Task %s prompt failed", req.TaskID), "failed", err.Error(), time.Since(session.StartedAt).Milliseconds())
		return
	}
	slog.Info("agent completed", "taskID", req.TaskID)
	r.publishStatusWithMetadata(req, "done", truncateSummary(summary), nil, sid)
	r.publishActivityEvent("agent", "Task Completed", fmt.Sprintf("Task %s completed (local)", req.TaskID), "success", truncateSummary(summary), time.Since(session.StartedAt).Milliseconds())
}

// runDockerAgent starts OpenCode inside a Docker container using the harness image.
// The container gets the workspace mounted, MCP socket mounted, and port mapped
// so the runner can communicate with OpenCode via HTTP (same as local mode).
func (r *Runner) runDockerAgent(ctx context.Context, session *task.TaskSession, req task.TaskRequest, socketPath string) {
	if req.Prompt == "" {
		r.publishStatusForRequest(req, "error", "no prompt provided", nil)
		return
	}

	configPath := filepath.Join(session.WorkspaceDir, ".opencode.json")

	// Pick a random host port for OpenCode serve.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("allocate port: %v", err), nil)
		return
	}
	hostPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	const containerPort = 9999
	containerName := "devfleet-task-" + req.TaskID

	// Clean up any stale container with the same name.
	exec.Command("docker", "rm", "-f", containerName).Run()

	secret := generatePassword()

	// Build docker run command in detached mode.
	dockerArgs := []string{
		"run", "-d",
		"--name", containerName,
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, containerPort),
		"-v", session.WorkspaceDir + ":/workspace",
		"-v", socketPath + ":" + socketPath,
		"-v", configPath + ":/opt/opencode/.config/opencode/config.json:ro",
		"-w", "/workspace",
		"-e", "TASK_ID=" + req.TaskID,
		"-e", "WORKSPACE=/workspace",
		"-e", "MCP_SOCKET_PATH=" + socketPath,
		"-e", "HOME=/opt/opencode",
		"-e", "XDG_CONFIG_HOME=/opt/opencode/.config",
		"-e", "XDG_DATA_HOME=/workspace/.local/share",
		"-e", "XDG_STATE_HOME=/workspace/.local/state",
		"-e", "XDG_CACHE_HOME=/workspace/.cache",
		"-e", "OPENCODE_CONFIG=/opt/opencode/.config/opencode/config.json",
		"-e", "OPENCODE_SERVER_PASSWORD=" + secret,
	}

	// Inject LLM env vars from the task request.
	for k, v := range req.Env {
		if isRunnerOwnedEnv(k) {
			continue
		}
		dockerArgs = append(dockerArgs, "-e", k+"="+v)
	}
	for _, key := range runnerOwnedEnvKeys() {
		if val := os.Getenv(key); val != "" {
			dockerArgs = append(dockerArgs, "-e", key+"="+val)
		}
	}

	dockerArgs = append(dockerArgs, req.AgentImage)
	dockerArgs = append(dockerArgs, "opencode", "serve", "--pure", "--hostname", "0.0.0.0", "--port", strconv.Itoa(containerPort))

	slog.Info("starting Docker container", "taskID", req.TaskID, "image", req.AgentImage, "hostPort", hostPort)
	r.publishStatusForRequest(req, "running", "Starting dev container...", nil)

	out, err := exec.CommandContext(ctx, "docker", dockerArgs...).CombinedOutput()
	if err != nil {
		slog.Error("docker run failed", "taskID", req.TaskID, "err", err, "output", string(out))
		r.publishStatusForRequest(req, "error", fmt.Sprintf("docker run: %v\n%s", err, string(out)), nil)
		return
	}

	// Ensure container is cleaned up when context is cancelled.
	defer func() {
		exec.Command("docker", "rm", "-f", containerName).Run()
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", hostPort)

	if err := waitForServeReady(baseURL, secret, serveReadyTimeout); err != nil {
		// Dump container logs for debugging.
		logs, _ := exec.Command("docker", "logs", containerName).CombinedOutput()
		slog.Error("opencode serve not ready in container", "taskID", req.TaskID, "err", err, "logs", string(logs))
		r.publishStatusForRequest(req, "error", fmt.Sprintf("container opencode serve not ready: %v", err), nil)
		return
	}
	slog.Info("container opencode serve ready", "taskID", req.TaskID, "url", baseURL)

	sid, err := createOpenCodeSession(baseURL, secret)
	if err != nil {
		r.publishStatusForRequest(req, "error", fmt.Sprintf("create session: %v", err), nil)
		return
	}
	slog.Info("session created", "taskID", req.TaskID, "sessionID", sid)

	eventsCtx, stopEvents := context.WithCancel(ctx)
	defer stopEvents()
	go r.watchOpenCodeEvents(eventsCtx, req.TaskID, baseURL, secret)

	r.publishStatusForRequest(req, "running", "Sending prompt to agent...", nil)
	summary, err := sendPromptAndWait(baseURL, sid, secret, req, taskPromptTimeout(req.TimeoutSec))
	if err != nil {
		r.publishStatusWithMetadata(req, "error", fmt.Sprintf("prompt failed: %v", err), nil, sid)
		r.publishActivityEvent("agent", "Task Failed", fmt.Sprintf("Task %s prompt failed", req.TaskID), "failed", err.Error(), time.Since(session.StartedAt).Milliseconds())
		return
	}
	slog.Info("agent completed", "taskID", req.TaskID)
	r.publishStatusWithMetadata(req, "done", truncateSummary(summary), nil, sid)
	r.publishActivityEvent("agent", "Task Completed", fmt.Sprintf("Task %s completed (docker)", req.TaskID), "success", truncateSummary(summary), time.Since(session.StartedAt).Milliseconds())
}

func (r *Runner) publishStatusWithMetadata(req task.TaskRequest, status, message string, artifacts []string, sessionID string) {
	resp := task.TaskResponse{
		TaskID:    req.TaskID,
		Status:    status,
		Artifacts: artifacts,
	}
	r.decorateTaskResponseForRequest(&resp, req, sessionID)
	if status != "running" {
		resp.StartedAt = time.Now()
		resp.EndedAt = time.Now()
	}
	if status == "error" || status == "cancelled" {
		resp.Error = message
	} else {
		resp.Summary = message
	}
	r.publishTaskResponse(resp)
}

func generatePassword() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func taskPromptTimeout(timeoutSec int) time.Duration {
	if timeoutSec <= 0 {
		timeoutSec = defaultTaskTimeoutSec
	}
	return time.Duration(timeoutSec) * time.Second
}

func (r *Runner) pipeOpenCodeOutput(taskID, stream string, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), opencodeEventLineMax)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		slog.Info("opencode output", "taskID", taskID, "stream", stream, "line", truncateSummary(line))
	}
	if err := scanner.Err(); err != nil {
		slog.Warn("opencode output read failed", "taskID", taskID, "stream", stream, "err", err)
	}
}

func (r *Runner) watchOpenCodeEvents(ctx context.Context, taskID, baseURL, secret string) {
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/event", nil)
	if err != nil {
		slog.Warn("opencode event request failed", "taskID", taskID, "err", err)
		return
	}
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("opencode event stream failed", "taskID", taskID, "err", err)
		}
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1000))
		slog.Warn("opencode event stream returned non-200", "taskID", taskID, "status", resp.StatusCode, "body", string(body))
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), opencodeEventLineMax)
	var dataLines []string
	lastPublished := time.Time{}
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if len(dataLines) > 0 {
				detail := summarizeOpenCodeEvent(strings.Join(dataLines, "\n"))
				if detail != "" {
					slog.Info("opencode event", "taskID", taskID, "detail", detail)
					if time.Since(lastPublished) >= 3*time.Second || strings.Contains(detail, "error") || strings.Contains(detail, "permission") {
						r.publishStatus(taskID, "running", "opencode: "+detail, nil)
						lastPublished = time.Now()
					}
				}
				dataLines = nil
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil && ctx.Err() == nil {
		slog.Warn("opencode event stream read failed", "taskID", taskID, "err", err)
	}
}

func summarizeOpenCodeEvent(raw string) string {
	var evt map[string]any
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		trimmed := strings.TrimSpace(raw)
		if len(trimmed) > 300 {
			trimmed = trimmed[:300] + "..."
		}
		return trimmed
	}
	typeName, _ := evt["type"].(string)
	if typeName == "" {
		return ""
	}
	props, _ := evt["properties"].(map[string]any)
	if props == nil {
		props, _ = evt["data"].(map[string]any)
	}
	switch typeName {
	case "session.status":
		return typeName + " " + compactJSON(props)
	case "session.error", "permission.asked", "permission.replied", "file.edited", "command.executed", "message.updated", "message.part.updated", "message.part.delta":
		return typeName + " " + compactJSON(props)
	default:
		return typeName
	}
}

func compactJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	text := string(data)
	if len(text) > 500 {
		return text[:500] + "..."
	}
	return text
}

func basicAuthHeader(password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("opencode:"+password))
}

func doPost(url, contentType, secret string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	if secret != "" {
		req.Header.Set("Authorization", basicAuthHeader(secret))
	}
	return http.DefaultClient.Do(req)
}

func waitForServeReady(baseURL, secret string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: serveHTTPTimeout}
	for time.Now().Before(deadline) {
		req, err := http.NewRequest("GET", baseURL+"/config", nil)
		if err != nil {
			time.Sleep(servePollInterval)
			continue
		}
		if secret != "" {
			req.Header.Set("Authorization", basicAuthHeader(secret))
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(servePollInterval)
	}
	return fmt.Errorf("server at %s not responding within %v", baseURL, timeout)
}

func createOpenCodeSession(baseURL, secret string) (string, error) {
	resp, err := doPost(baseURL+"/session", "application/json", secret, strings.NewReader("{}"))
	if err != nil {
		return "", fmt.Errorf("POST /session: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("POST /session: status %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode session: %w", err)
	}
	return result.ID, nil
}

func sendPromptAndWait(baseURL, sessionID, secret string, req task.TaskRequest, timeout time.Duration) (string, error) {
	providerID, modelID := promptModel(req, "synthetic", "hf:zai-org/GLM-5.1")
	variantID := promptVariant(req)
	slog.Info("sendPromptAndWait model", "provider", providerID, "model", modelID, "variant", variantID, "agent", req.Agent)
	model := map[string]any{
		"providerID": providerID,
		"modelID":    modelID,
	}
	payload, _ := json.Marshal(map[string]any{
		"parts": []map[string]any{
			{"type": "text", "text": promptWithSkillHints(req.Prompt, req.Skills)},
		},
		"model": model,
	})

	url := baseURL + "/session/" + sessionID + "/message"
	httpClient := &http.Client{Timeout: timeout}
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if secret != "" {
		httpReq.Header.Set("Authorization", basicAuthHeader(secret))
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("POST /message: %w", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	slog.Info("message response", "status", resp.StatusCode, "len", len(respBody))

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("POST /message: status %d: %s", resp.StatusCode, string(respBody))
	}

	var msg struct {
		Info struct {
			Role string `json:"role"`
		} `json:"info"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(respBody, &msg); err != nil {
		return "", fmt.Errorf("decode message: %w (body: %s)", err, string(respBody[:min(len(respBody), 500)]))
	}

	var texts []string
	for _, part := range msg.Parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			texts = append(texts, part.Text)
		}
	}
	if len(texts) > 0 {
		return strings.Join(texts, "\n"), nil
	}
	return "", fmt.Errorf("no text in assistant response (parts: %d)", len(msg.Parts))
}
