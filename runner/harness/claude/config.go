package claude

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/flatout-works/chetter/runner/harness/mcpconfig"
	"github.com/flatout-works/chetter/runner/internal/safefs"
	"github.com/flatout-works/chetter/runner/internal/task"
)

func GenerateConfigForTask(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	return GenerateConfigForTaskWithRunnerToken(wsDir, runnerMCPURL, "", chetterMCPURL, chetterMCPToken, req, isLocal)
}

func GenerateConfigForTaskWithRunnerToken(wsDir, runnerMCPURL, runnerMCPToken, chetterMCPURL, chetterMCPToken string, req task.TaskRequest, isLocal bool) error {
	if err := safefs.EnsureDir(wsDir, ".claude", 0750); err != nil {
		return err
	}

	settings := map[string]any{
		"permissions": map[string]any{
			"allow": []string{
				"Bash(ls:*)",
				"Bash(find:*)",
				"Bash(git:*)",
				"Bash(make:*)",
				"Bash(gh:*)",
				"Bash(go:*)",
				"Bash(cat:*)",
				"Bash(jq:*)",
				"Bash(sed:*)",
				"Bash(grep:*)",
				"Bash(curl:*)",
				"Bash(date:*)",
				"Bash(echo:*)",
				"Bash(mkdir:*)",
				"Bash(cp:*)",
				"Bash(mv:*)",
				"Bash(rm:*)",
				"Bash(chmod:*)",
				"Bash(chown:*)",
				"Bash(ln:*)",
				"Bash(tar:*)",
				"Bash(unzip:*)",
				"Bash(head:*)",
				"Bash(tail:*)",
				"Bash(sort:*)",
				"Bash(uniq:*)",
				"Bash(wc:*)",
				"Read",
				"Edit",
				"Glob",
				"Grep",
				"Write",
			},
			"deny": []string{
				"Bash(docker:*)",
				"Bash(systemctl:*)",
				"Bash(pkill:*)",
				"Bash(kill:*)",
				"Bash(shutdown:*)",
				"Bash(reboot:*)",
			},
		},
		"skipPermissionsOnAllowed": true,
	}

	const settingsRelPath = ".claude/settings.json"
	settingsData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := safefs.WriteFile(wsDir, settingsRelPath, settingsData, 0644); err != nil {
		return err
	}
	slog.Info("wrote claude settings", "path", filepath.Join(wsDir, settingsRelPath))

	mcpServers := map[string]any{}

	if runnerMCPURL != "" {
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
		chetterMCP := map[string]any{
			"type":    "http",
			"url":     chetterMCPURL,
			"enabled": true,
		}
		if chetterMCPToken != "" {
			chetterMCP["headers"] = map[string]string{
				"Authorization": "Bearer " + chetterMCPToken,
			}
		}
		mcpServers["chetter"] = chetterMCP
	}
	if err := mcpconfig.AddHTTPServers(mcpServers, req.MCPProfiles); err != nil {
		return err
	}

	if len(mcpServers) > 0 {
		agentMCP := map[string]any{
			"mcpServers": mcpServers,
		}
		agentMCPData, err := json.MarshalIndent(agentMCP, "", "  ")
		if err != nil {
			return err
		}
		const agentMCPRelPath = ".claude/mcp.json"
		if err := safefs.WriteFile(wsDir, agentMCPRelPath, agentMCPData, 0644); err != nil {
			return err
		}
		slog.Info("wrote claude mcp config", "path", filepath.Join(wsDir, agentMCPRelPath))
	}

	if isLocal {
		copyClaudeState(wsDir)
	}

	return nil
}

func copyClaudeState(wsDir string) {
	copyFirstExisting("claude auth state", wsDir, ".claude/auth.json", candidateClaudeAuthPaths())
}

func candidateClaudeAuthPaths() []string {
	home := os.Getenv("HOME")
	return []string{
		home + "/.claude/auth.json",
		home + "/.config/claude/auth.json",
	}
}

func copyFirstExisting(label, wsDir, relDst string, candidates []string) {
	for _, src := range candidates {
		if _, err := os.Stat(src); err == nil {
			data, err := os.ReadFile(src)
			if err != nil {
				slog.Warn("copy state read warning", "label", label, "src", src, "err", err)
				continue
			}
			if err := safefs.WriteFile(wsDir, relDst, data, 0600); err != nil {
				slog.Warn("copy state write warning", "label", label, "dst", filepath.Join(wsDir, relDst), "err", err)
				continue
			}
			slog.Info("copied state", "label", label, "src", src, "dst", filepath.Join(wsDir, relDst), "bytes", len(data))
			return
		}
	}
	slog.Info("no state file found for copy", "label", label)
}
