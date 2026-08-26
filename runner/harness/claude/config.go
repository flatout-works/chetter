package claude

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/flatout-works/chetter/runner/harness/mcpconfig"
	"github.com/flatout-works/chetter/runner/internal/skilltar"
	"github.com/flatout-works/chetter/runner/internal/task"
)

func GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	claudeDir := wsDir + "/.claude"
	if err := os.MkdirAll(claudeDir, 0750); err != nil {
		return err
	}

	// Claude Code headless runs can never answer a permission prompt, so every
	// "ask" is a silent "deny". Claude's Bash matcher also splits compound
	// commands on ;/&& and rejects pipelines, command substitution, and
	// redirects unless every segment matches, which made the old per-binary
	// allowlist a random denial generator (gofmt, shell chains, and gh calls
	// with $(...) were blocked mid-task). Allow Bash wholesale and keep the
	// deny list as the real control: it is evaluated before allow, and the
	// gVisor task container is the actual security boundary.
	allow := []string{
		"Bash",
		"Read",
		"Edit",
		"Glob",
		"Grep",
		"Write",
	}
	enabledMCPServers := make([]string, 0, len(req.McpEndpoints)+2)
	for _, endpoint := range req.McpEndpoints {
		allow = append(allow, "mcp__"+endpoint.Name+"__*")
		enabledMCPServers = append(enabledMCPServers, endpoint.Name)
	}
	if runnerMCPURL != "" {
		allow = append(allow, "mcp__runner-bridge__*")
		enabledMCPServers = append(enabledMCPServers, "runner-bridge")
	}
	if chetterMCPURL != "" {
		allow = append(allow, "mcp__chetter__*")
		enabledMCPServers = append(enabledMCPServers, "chetter")
	}

	settings := map[string]any{
		"permissions": map[string]any{
			"allow": allow,
			// Deny is evaluated before allow, so these still block even with
			// the bare "Bash" allow above. Anything that escapes the task
			// container (docker socket, host package/runtime control) or can
			// never produce output an agent can use must stay here.
			"deny": []string{
				"AskUserQuestion",
				"Bash(docker:*)",
				"Bash(podman:*)",
				"Bash(systemctl:*)",
				"Bash(journalctl:*)",
				"Bash(pkill:*)",
				"Bash(kill:*)",
				"Bash(shutdown:*)",
				"Bash(reboot:*)",
				"Bash(ssh:*)",
				"Bash(scp:*)",
				"Bash(sudo:*)",
			},
		},
	}
	if len(enabledMCPServers) > 0 {
		// Project MCP servers otherwise wait for interactive approval, which is
		// unavailable to the headless serve proxy. Under --strict-mcp-config
		// this list is inert, but local mode still benefits from it.
		settings["enabledMcpjsonServers"] = enabledMCPServers
	}

	settingsPath := claudeDir + "/settings.json"
	settingsData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(settingsPath, settingsData, 0600); err != nil {
		return err
	}
	slog.Info("wrote claude settings", "path", settingsPath)

	mcpServers := map[string]any{}

	if runnerMCPURL != "" {
		runnerMCP := map[string]any{
			"type": "http",
			"url":  runnerMCPURL,
		}
		mcpconfig.SetBearerToken(runnerMCP, req.RunnerMCPToken)
		mcpServers["runner-bridge"] = runnerMCP
	}

	if chetterMCPURL != "" {
		chetterMCP := map[string]any{
			"type": "http",
			"url":  chetterMCPURL,
		}
		mcpconfig.SetBearerToken(chetterMCP, chetterMCPToken)
		mcpServers["chetter"] = chetterMCP
	}

	if len(req.McpEndpoints) > 0 {
		if err := mcpconfig.AddClaudeServers(mcpServers, req.McpEndpoints); err != nil {
			return err
		}
	}

	if len(mcpServers) > 0 {
		agentMCP := map[string]any{
			"mcpServers": mcpServers,
		}
		agentMCPData, err := json.MarshalIndent(agentMCP, "", "  ")
		if err != nil {
			return err
		}
		agentMCPPath := filepath.Join(wsDir, ".mcp.json")
		if err := mcpconfig.WritePrivateFile(agentMCPPath, agentMCPData); err != nil {
			return err
		}
		slog.Info("wrote claude mcp config", "path", agentMCPPath)

		// The serve proxy passes this file via --strict-mcp-config so only the
		// runner-generated servers load: -p mode loads a cloned repository's
		// .mcp.json without any approval, which would otherwise let a
		// malicious repo register its own MCP server. The runner-bridge and
		// chetter entries carry bearer tokens, hence the private mode.
		strictMCPPath := filepath.Join(claudeDir, "chetter-mcp.json")
		if err := mcpconfig.WritePrivateFile(strictMCPPath, agentMCPData); err != nil {
			return fmt.Errorf("write claude strict mcp config: %w", err)
		}
		slog.Info("wrote claude strict mcp config", "path", strictMCPPath)
	}

	if isLocal {
		copyClaudeState(wsDir)
	}

	writeAgentAndSkillDefinitions(claudeDir, req)

	return nil
}

// writeAgentAndSkillDefinitions materializes the task's agent persona and
// skill definitions where Claude Code discovers them: project subagents in
// .claude/agents/<name>.md and skills in .claude/skills/<name>/. The agent
// file doubles as the --append-system-prompt-file source for the serve proxy.
func writeAgentAndSkillDefinitions(claudeDir string, req task.TaskRequest) {
	if req.AgentDefinition != "" && req.Agent != "" {
		agentDir := filepath.Join(claudeDir, "agents")
		if err := os.MkdirAll(agentDir, 0750); err != nil {
			slog.Warn("create claude agent dir", "err", err)
		} else {
			path := filepath.Join(agentDir, req.Agent+".md")
			if err := os.WriteFile(path, []byte(req.AgentDefinition), 0644); err != nil {
				slog.Warn("write claude agent definition", "agent", req.Agent, "err", err)
			} else {
				slog.Info("injected claude agent definition", "agent", req.Agent, "path", path)
			}
		}
	}
	if len(req.SkillDefinitions) > 0 {
		skillsBase := filepath.Join(claudeDir, "skills")
		for name, tarBytes := range req.SkillDefinitions {
			skillDir := filepath.Join(skillsBase, name)
			if err := os.MkdirAll(skillDir, 0750); err != nil {
				slog.Warn("create claude skill dir", "skill", name, "err", err)
				continue
			}
			if err := skilltar.Extract(tarBytes, skillDir); err != nil {
				slog.Warn("extract claude skill", "skill", name, "err", err)
			} else {
				slog.Info("injected claude skill", "skill", name, "dir", skillDir, "bytes", len(tarBytes))
			}
		}
	}
}

func copyClaudeState(wsDir string) {
	copyFirstExisting("claude auth state", wsDir+"/.claude/auth.json", candidateClaudeAuthPaths())
}

func candidateClaudeAuthPaths() []string {
	home := os.Getenv("HOME")
	return []string{
		home + "/.claude/auth.json",
		home + "/.config/claude/auth.json",
	}
}

func copyFirstExisting(label, dst string, candidates []string) {
	for _, src := range candidates {
		if _, err := os.Stat(src); err == nil {
			if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
				slog.Warn("copy state mkdir warning", "label", label, "err", err)
				continue
			}
			data, err := os.ReadFile(src)
			if err != nil {
				slog.Warn("copy state read warning", "label", label, "src", src, "err", err)
				continue
			}
			if err := os.WriteFile(dst, data, 0600); err != nil {
				slog.Warn("copy state write warning", "label", label, "dst", dst, "err", err)
				continue
			}
			slog.Info("copied state", "label", label, "src", src, "dst", dst, "bytes", len(data))
			return
		}
	}
	slog.Info("no state file found for copy", "label", label)
}
