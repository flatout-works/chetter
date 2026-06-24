package claude

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
)

func GenerateConfig(wsDir, runnerMCPURL, chetterMCPURL, chetterMCPToken string, isLocal bool) error {
	claudeDir := wsDir + "/.claude"
	if err := os.MkdirAll(claudeDir, 0750); err != nil {
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

	settingsPath := claudeDir + "/settings.json"
	settingsData, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(settingsPath, settingsData, 0644); err != nil {
		return err
	}
	slog.Info("wrote claude settings", "path", settingsPath)

	mcpServers := map[string]any{}

	if runnerMCPURL != "" {
		mcpServers["runner-bridge"] = map[string]any{
			"type":    "remote",
			"url":     runnerMCPURL,
			"enabled": true,
		}
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

	if len(mcpServers) > 0 {
		agentMCP := map[string]any{
			"mcpServers": mcpServers,
		}
		agentMCPData, err := json.MarshalIndent(agentMCP, "", "  ")
		if err != nil {
			return err
		}
		agentMCPPath := claudeDir + "/mcp.json"
		if err := os.WriteFile(agentMCPPath, agentMCPData, 0644); err != nil {
			return err
		}
		slog.Info("wrote claude mcp config", "path", agentMCPPath)
	}

	if isLocal {
		copyClaudeState(wsDir)
	}

	return nil
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
