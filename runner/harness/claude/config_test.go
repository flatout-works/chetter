package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func TestGenerateConfigDeniesInteractiveQuestions(t *testing.T) {
	wsDir := t.TempDir()
	if err := GenerateConfig(wsDir, "", "", "", task.TaskRequest{}, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wsDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	if !contains(settings.Permissions.Deny, "AskUserQuestion") {
		t.Fatalf("expected AskUserQuestion to be denied, got %q", settings.Permissions.Deny)
	}
}

// TestGenerateConfigAllowsBashButDeniesContainerEscape documents the headless
// permission model: Bash must be allowed wholesale (a partial binary allowlist
// silently denied gofmt, compound commands, and $(...) forms mid-task), while
// the deny list captures commands that escape or break the task container.
func TestGenerateConfigAllowsBashButDeniesContainerEscape(t *testing.T) {
	wsDir := t.TempDir()
	if err := GenerateConfig(wsDir, "", "", "", task.TaskRequest{}, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wsDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	if !contains(settings.Permissions.Allow, "Bash") {
		t.Fatalf("expected bare Bash allow for headless tasks, got %q", settings.Permissions.Allow)
	}
	for _, wantBinary := range []string{"docker", "systemctl", "pkill", "kill", "shutdown", "reboot", "sudo", "ssh"} {
		if !contains(settings.Permissions.Deny, "Bash("+wantBinary+":*)") {
			t.Fatalf("expected Bash(%s:*) to be denied, got %q", wantBinary, settings.Permissions.Deny)
		}
	}
}

func TestGenerateConfigIncludesRunnerBridgeHTTPServer(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{RunnerMCPToken: "runner-token"}
	if err := GenerateConfig(wsDir, "http://runner.test/mcp", "http://chetter.test/mcp", "chetter-token", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	mcpPath := filepath.Join(wsDir, ".mcp.json")
	mcpData, err := os.ReadFile(mcpPath)
	if err != nil {
		t.Fatalf("read MCP config: %v", err)
	}
	info, err := os.Stat(mcpPath)
	if err != nil {
		t.Fatalf("stat MCP config: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("MCP config permissions = %v, want 0600", info.Mode().Perm())
	}
	var mcpConfig struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(mcpData, &mcpConfig); err != nil {
		t.Fatalf("parse MCP config: %v", err)
	}
	bridge := mcpConfig.MCPServers["runner-bridge"]
	if bridge["type"] != "http" {
		t.Fatalf("runner bridge type = %v, want http", bridge["type"])
	}
	if bridge["url"] != "http://runner.test/mcp" {
		t.Fatalf("runner bridge URL = %v", bridge["url"])
	}
	headers, ok := bridge["headers"].(map[string]any)
	if !ok || headers["Authorization"] != "Bearer runner-token" {
		t.Fatalf("runner bridge headers = %#v", bridge["headers"])
	}
	chetter := mcpConfig.MCPServers["chetter"]
	if chetter["type"] != "http" || chetter["url"] != "http://chetter.test/mcp" {
		t.Fatalf("chetter MCP server = %#v", chetter)
	}
	chetterHeaders, ok := chetter["headers"].(map[string]any)
	if !ok || chetterHeaders["Authorization"] != "Bearer chetter-token" {
		t.Fatalf("chetter MCP headers = %#v", chetter["headers"])
	}

	settingsData, err := os.ReadFile(filepath.Join(wsDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		EnabledMCPServers []string `json:"enabledMcpjsonServers"`
		Permissions       struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(settingsData, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	for _, server := range []string{"runner-bridge", "chetter"} {
		if !contains(settings.EnabledMCPServers, server) {
			t.Fatalf("MCP server %q approval missing: %q", server, settings.EnabledMCPServers)
		}
		if !contains(settings.Permissions.Allow, "mcp__"+server+"__*") {
			t.Fatalf("MCP server %q permission missing: %q", server, settings.Permissions.Allow)
		}
	}
}

func TestGenerateConfigSettingsArePrivateWithoutUndocumentedKeys(t *testing.T) {
	wsDir := t.TempDir()
	if err := GenerateConfig(wsDir, "", "", "", task.TaskRequest{}, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	settingsPath := filepath.Join(wsDir, ".claude", "settings.json")
	info, err := os.Stat(settingsPath)
	if err != nil {
		t.Fatalf("stat settings: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("settings permissions = %v, want 0600", info.Mode().Perm())
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	if _, ok := settings["skipPermissionsOnAllowed"]; ok {
		t.Fatalf("settings must not rely on undocumented skipPermissionsOnAllowed: %s", data)
	}
	if _, err := os.Stat(filepath.Join(wsDir, ".claude", "chetter-mcp.json")); !os.IsNotExist(err) {
		t.Fatalf("strict MCP config must not exist without MCP servers, err=%v", err)
	}
}

func TestGenerateConfigWritesStrictMCPConfig(t *testing.T) {
	wsDir := t.TempDir()
	req := task.TaskRequest{RunnerMCPToken: "runner-token"}
	if err := GenerateConfig(wsDir, "http://runner.test/mcp", "", "", req, false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	strictPath := filepath.Join(wsDir, ".claude", "chetter-mcp.json")
	data, err := os.ReadFile(strictPath)
	if err != nil {
		t.Fatalf("read strict MCP config: %v", err)
	}
	info, err := os.Stat(strictPath)
	if err != nil {
		t.Fatalf("stat strict MCP config: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("strict MCP config permissions = %v, want 0600", info.Mode().Perm())
	}
	var strictConfig struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(data, &strictConfig); err != nil {
		t.Fatalf("parse strict MCP config: %v", err)
	}
	bridge := strictConfig.MCPServers["runner-bridge"]
	if bridge["url"] != "http://runner.test/mcp" {
		t.Fatalf("strict MCP runner bridge = %#v", bridge)
	}
	headers, ok := bridge["headers"].(map[string]any)
	if !ok || headers["Authorization"] != "Bearer runner-token" {
		t.Fatalf("strict MCP runner bridge headers = %#v", bridge["headers"])
	}

	// The strict file must mirror the interactive .mcp.json server set.
	mcpData, err := os.ReadFile(filepath.Join(wsDir, ".mcp.json"))
	if err != nil {
		t.Fatalf("read MCP config: %v", err)
	}
	var mcpConfig struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(mcpData, &mcpConfig); err != nil {
		t.Fatalf("parse MCP config: %v", err)
	}
	if len(strictConfig.MCPServers) != len(mcpConfig.MCPServers) {
		t.Fatalf("strict MCP server count = %d, want %d", len(strictConfig.MCPServers), len(mcpConfig.MCPServers))
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
