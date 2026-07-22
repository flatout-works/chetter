package definitions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanDefinitions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "global/agents/pr-reviewer.md", "---\nidentity: primary-bot\n---\n# PR reviewer\n")
	writeFile(t, root, "global/skills/chetter/SKILL.md", "# Chetter skill\n")
	writeFile(t, root, "global/skills/flat.md", "# Flat skill\n")
	writeFile(t, root, "global/triggers/nightly.yaml", "name: nightly\n")
	writeFile(t, root, "global/task-templates/improve.md", "Improve this\n")

	m := New("", "", root)
	defs, err := m.ScanDefinitions()
	if err != nil {
		t.Fatalf("scan definitions: %v", err)
	}
	if len(defs) != 5 {
		t.Fatalf("expected 5 definitions, got %d: %#v", len(defs), defs)
	}
	assertDefinition(t, defs, DefinitionTypeAgent, "pr-reviewer", "global/agents/pr-reviewer.md")
	assertDefinition(t, defs, DefinitionTypeSkill, "chetter", "global/skills/chetter/SKILL.md")
	assertDefinition(t, defs, DefinitionTypeSkill, "flat", "global/skills/flat.md")
	assertDefinition(t, defs, DefinitionTypeTrigger, "nightly", "global/triggers/nightly.yaml")
	assertDefinition(t, defs, DefinitionTypeTaskTemplate, "improve", "global/task-templates/improve.md")
	for _, def := range defs {
		if def.ContentHash == "" {
			t.Fatalf("definition %s/%s has empty hash", def.Type, def.Name)
		}
	}
}

func TestScanDefinitionsRejectsInvalidTriggerYAML(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "global/triggers/bad.yaml", "name: bad\nunknown: true\n")

	m := New("", "", root)
	_, err := m.ScanDefinitions()
	if err == nil {
		t.Fatal("expected invalid trigger yaml to fail")
	}
	if !strings.Contains(err.Error(), "global/triggers/bad.yaml") || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestScanDefinitionsIgnoresRootDefinitionDirectories(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/ignored.md", "---\nidentity: primary-bot\n---\n# Ignored\n")
	writeFile(t, root, "skills/ignored/SKILL.md", "# Ignored\n")
	writeFile(t, root, "triggers/ignored.yaml", "name: ignored\n")
	writeFile(t, root, "mcp-endpoints/ignored.yaml", "name: ignored\nurl: https://mcp.example.com\n")
	writeFile(t, root, "task-templates/ignored.md", "Ignored\n")

	defs, err := New("", "", root).ScanDefinitions()
	if err != nil {
		t.Fatalf("scan definitions: %v", err)
	}
	if len(defs) != 0 {
		t.Fatalf("root definition directories should be ignored, got %#v", defs)
	}
}

func TestValidateAgentDefinitionFrontmatter(t *testing.T) {
	valid := `---
description: Reviews pull requests.
model: synthetic/hf:zai-org/GLM-5.2
mode: primary
identity: primary-bot
permission:
  edit: allow
---

# Agent
`
	if err := ValidateAgentDefinition(valid); err != nil {
		t.Fatalf("valid agent frontmatter failed: %v", err)
	}
	if err := ValidateAgentDefinition("# Plain markdown\n"); err == nil {
		t.Fatal("plain markdown without an identity should be rejected")
	}
	invalid := `---
description:
  - not a string
---
`
	if err := ValidateAgentDefinition(invalid); err == nil {
		t.Fatal("expected invalid agent frontmatter to fail")
	}
}

func TestParseTriggerYAMLCopiesTopLevelRuntimeConfig(t *testing.T) {
	trigger, err := ParseTriggerYAML(`name: issue-handler
trigger_type: issue
repo: flatout-works/chetter
event: comment
match_labels:
  - bug
session_mode: resumable
pause_reason: waiting
ttl_hours: 24
`)
	if err != nil {
		t.Fatalf("ParseTriggerYAML: %v", err)
	}
	for _, want := range []string{
		`"repo":"flatout-works/chetter"`,
		`"event":"comment"`,
		`"match_labels":["bug"]`,
		`"session_mode":"resumable"`,
		`"pause_reason":"waiting"`,
		`"ttl_hours":24`,
	} {
		if !strings.Contains(trigger.TriggerCfg, want) {
			t.Fatalf("trigger_config %s missing %s", trigger.TriggerCfg, want)
		}
	}
}

func TestParseTriggerYAMLAllowsDisabledTrigger(t *testing.T) {
	trigger, err := ParseTriggerYAML("name: disabled\nenabled: false\n")
	if err != nil {
		t.Fatalf("ParseTriggerYAML: %v", err)
	}
	if trigger.Enabled {
		t.Fatal("expected enabled: false to be preserved")
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func assertDefinition(t *testing.T, defs []Definition, definitionType, name, path string) {
	t.Helper()
	for _, def := range defs {
		if def.Type == definitionType && def.Name == name && def.Path == path {
			return
		}
	}
	t.Fatalf("missing definition type=%s name=%s path=%s in %#v", definitionType, name, path, defs)
}

func TestScanDefinitionsIncludesGlobalAndTeamMcpEndpoints(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "global/mcp-endpoints/global-ep.yaml", "name: global-ep\nurl: https://mcp.example.com/global\n")
	writeFile(t, root, "global/mcp-endpoints/global2.yml", "name: global2\nurl: https://mcp.example.com/global2\n")
	writeFile(t, root, "groups/engineering/mcp-endpoints/team-ep.yaml", "name: team-ep\nurl: https://mcp.example.com/team\n")

	defs, err := New("", "", root).ScanDefinitions()
	if err != nil {
		t.Fatalf("scan definitions: %v", err)
	}
	assertDefinition(t, defs, DefinitionTypeMCPEndpoint, "global-ep", "global/mcp-endpoints/global-ep.yaml")
	assertDefinition(t, defs, DefinitionTypeMCPEndpoint, "global2", "global/mcp-endpoints/global2.yml")
	assertDefinition(t, defs, DefinitionTypeMCPEndpoint, "team-ep", "groups/engineering/mcp-endpoints/team-ep.yaml")
	for _, def := range defs {
		if def.Type == DefinitionTypeMCPEndpoint && def.Scope == DefinitionScopeTeam && def.TeamName != "engineering" {
			t.Fatalf("team endpoint has wrong team: %#v", def)
		}
	}
}

func TestScanDefinitionsRejectsRepoScopedMcpEndpoints(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "repos/acme/app/mcp-endpoints/repo-ep.yml", "name: repo-ep\nurl: https://mcp.example.com/repo\n")

	_, err := New("", "", root).ScanDefinitions()
	if err == nil || !strings.Contains(err.Error(), "MCP endpoints are global or team scoped") {
		t.Fatalf("expected repo-scoped endpoint rejection, got %v", err)
	}
}

func TestScanDefinitionsRejectsMcpEndpointNameMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "global/mcp-endpoints/file-name.yaml", "name: other-name\nurl: https://mcp.example.com\n")

	_, err := New("", "", root).ScanDefinitions()
	if err == nil || !strings.Contains(err.Error(), `endpoint name "other-name" must match file name "file-name"`) {
		t.Fatalf("expected name mismatch error, got %v", err)
	}
}

func TestParseMCPEndpointYAML(t *testing.T) {
	endpoint, err := ParseMCPEndpointYAML(`name: context
transport: http
url: https://mcp.example.com/mcp
headers:
  X-Tenant: engineering
auth:
  type: bearer
  token_env: EXAMPLE_MCP_TOKEN
`)
	if err != nil {
		t.Fatalf("ParseMCPEndpointYAML: %v", err)
	}
	if endpoint.Name != "context" || endpoint.Transport != "http" || endpoint.URL != "https://mcp.example.com/mcp" {
		t.Fatalf("unexpected endpoint: %#v", endpoint)
	}
	if endpoint.Auth == nil || endpoint.Auth.Type != "bearer" || endpoint.Auth.TokenEnv != "EXAMPLE_MCP_TOKEN" {
		t.Fatalf("unexpected auth: %#v", endpoint.Auth)
	}
	if endpoint.Headers["X-Tenant"] != "engineering" {
		t.Fatalf("unexpected headers: %#v", endpoint.Headers)
	}
}

func TestParseMCPEndpointYAMLRejectsUnsafeCredentials(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"literal bearer token", "name: bad\nurl: https://mcp.example.com\nauth:\n  type: bearer\n  token: literal-secret\n", "field token not found"},
		{"literal authorization header", "name: bad\nurl: https://mcp.example.com\nheaders:\n  Authorization: Bearer literal-secret\n", "auth.token_env"},
		{"invalid token env", "name: bad\nurl: https://mcp.example.com\nauth:\n  type: bearer\n  token_env: lower-case\n", "valid environment variable name"},
		{"url credentials", "name: bad\nurl: https://user:secret@mcp.example.com\n", "must not contain credentials"},
		{"reserved name", "name: chetter\nurl: https://mcp.example.com/mcp\n", "is reserved"},
		{"invalid transport", "name: bad\nurl: https://mcp.example.com\ntransport: ws\n", "transport must be http or sse"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseMCPEndpointYAML(tt.content)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestValidateAgentDefinitionWithMcpEndpoints(t *testing.T) {
	valid := `---
identity: primary-bot
mcp_endpoints:
  - context
  - github
---
# Agent with MCP endpoints
`
	if err := ValidateAgentDefinition(valid); err != nil {
		t.Fatalf("valid agent with mcp_endpoints failed: %v", err)
	}

	invalid := `---
identity: primary-bot
mcp_endpoints:
  - context
  - 123
---
# Bad mcp_endpoints
`
	if err := ValidateAgentDefinition(invalid); err == nil {
		t.Fatal("expected non-string mcp_endpoints item to fail")
	}

	invalidType := `---
identity: primary-bot
mcp_endpoints: not-a-list
---
# Bad mcp_endpoints type
`
	if err := ValidateAgentDefinition(invalidType); err == nil {
		t.Fatal("expected non-list mcp_endpoints to fail")
	}
}

func TestAgentMcpEndpoints(t *testing.T) {
	content := `---
identity: primary-bot
mcp_endpoints:
  - context
  - github
---
# Agent
`
	names, err := AgentMcpEndpoints(content)
	if err != nil {
		t.Fatalf("AgentMcpEndpoints: %v", err)
	}
	if len(names) != 2 || names[0] != "context" || names[1] != "github" {
		t.Fatalf("unexpected endpoint names: %#v", names)
	}

	noneContent := `---
identity: primary-bot
---
# Agent without endpoints
`
	names, err = AgentMcpEndpoints(noneContent)
	if err != nil {
		t.Fatalf("AgentMcpEndpoints without endpoints: %v", err)
	}
	if names != nil {
		t.Fatalf("expected nil for agent without endpoints, got %#v", names)
	}
}
