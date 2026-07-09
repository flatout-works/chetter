package definitions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanDefinitions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/pr-reviewer.md", "# PR reviewer\n")
	writeFile(t, root, "skills/chetter/SKILL.md", "# Chetter skill\n")
	writeFile(t, root, "skills/flat.md", "# Flat skill\n")
	writeFile(t, root, "triggers/nightly.yaml", "name: nightly\n")
	writeFile(t, root, "task-templates/improve.md", "Improve this\n")

	m := New("", "", root)
	defs, err := m.ScanDefinitions()
	if err != nil {
		t.Fatalf("scan definitions: %v", err)
	}
	if len(defs) != 5 {
		t.Fatalf("expected 5 definitions, got %d: %#v", len(defs), defs)
	}
	assertDefinition(t, defs, DefinitionTypeAgent, "pr-reviewer", "agents/pr-reviewer.md")
	assertDefinition(t, defs, DefinitionTypeSkill, "chetter", "skills/chetter/SKILL.md")
	assertDefinition(t, defs, DefinitionTypeSkill, "flat", "skills/flat.md")
	assertDefinition(t, defs, DefinitionTypeTrigger, "nightly", "triggers/nightly.yaml")
	assertDefinition(t, defs, DefinitionTypeTaskTemplate, "improve", "task-templates/improve.md")
	for _, def := range defs {
		if def.ContentHash == "" {
			t.Fatalf("definition %s/%s has empty hash", def.Type, def.Name)
		}
	}
}

func TestScanDefinitionsRejectsInvalidTriggerYAML(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "triggers/bad.yaml", "name: bad\nunknown: true\n")

	m := New("", "", root)
	_, err := m.ScanDefinitions()
	if err == nil {
		t.Fatal("expected invalid trigger yaml to fail")
	}
	if !strings.Contains(err.Error(), "triggers/bad.yaml") || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestScanDefinitionsIncludesOnlyGlobalMCPProfiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "mcp-profiles/root-profile.yaml", "name: root-profile\nurl: https://mcp.example.com/root\n")
	writeFile(t, root, "global/mcp-profiles/global-profile.yml", "name: global-profile\nurl: https://mcp.example.com/global\n")

	defs, err := New("", "", root).ScanDefinitions()
	if err != nil {
		t.Fatalf("scan definitions: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("expected only two global MCP profiles, got %d: %#v", len(defs), defs)
	}
	assertDefinition(t, defs, DefinitionTypeMCPProfile, "root-profile", "mcp-profiles/root-profile.yaml")
	assertDefinition(t, defs, DefinitionTypeMCPProfile, "global-profile", "global/mcp-profiles/global-profile.yml")
	for _, def := range defs {
		if def.Scope != DefinitionScopeGlobal {
			t.Fatalf("MCP profile must be global: %#v", def)
		}
	}
}

func TestScanDefinitionsRejectsScopedMCPProfiles(t *testing.T) {
	for _, path := range []string{
		"groups/engineering/mcp-profiles/team-profile.yaml",
		"repos/acme/app/mcp-profiles/repo-profile.yml",
	} {
		t.Run(path, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, root, path, "name: scoped\nurl: https://mcp.example.com/mcp\n")
			_, err := New("", "", root).ScanDefinitions()
			if err == nil || !strings.Contains(err.Error(), "MCP profiles are global-only") {
				t.Fatalf("expected scoped profile rejection, got %v", err)
			}
		})
	}
}

func TestScanDefinitionsRejectsDuplicateGlobalMCPProfileNames(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "mcp-profiles/context.yaml", "name: context\nurl: https://one.example.com/mcp\n")
	writeFile(t, root, "global/mcp-profiles/context.yml", "name: context\nurl: https://two.example.com/mcp\n")

	_, err := New("", "", root).ScanDefinitions()
	if err == nil || !strings.Contains(err.Error(), `duplicate global MCP profile name "context"`) {
		t.Fatalf("expected duplicate profile error, got %v", err)
	}
}

func TestScanDefinitionsRejectsMCPProfileNameMismatch(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "mcp-profiles/file-name.yaml", "name: other-name\nurl: https://mcp.example.com\n")

	_, err := New("", "", root).ScanDefinitions()
	if err == nil || !strings.Contains(err.Error(), `profile name "other-name" must match file name "file-name"`) {
		t.Fatalf("expected file name mismatch, got %v", err)
	}
}

func TestParseMCPProfileYAMLBearerTokenEnv(t *testing.T) {
	profile, err := ParseMCPProfileYAML(`name: context
transport: http
url: https://mcp.example.com/mcp
headers:
  X-Tenant: engineering
auth:
  type: bearer
  token_env: EXAMPLE_MCP_TOKEN
`)
	if err != nil {
		t.Fatalf("ParseMCPProfileYAML: %v", err)
	}
	if profile.Name != "context" || profile.Transport != "http" || profile.URL != "https://mcp.example.com/mcp" {
		t.Fatalf("unexpected profile: %#v", profile)
	}
	if profile.Auth == nil || profile.Auth.Type != "bearer" || profile.Auth.TokenEnv != "EXAMPLE_MCP_TOKEN" {
		t.Fatalf("unexpected auth: %#v", profile.Auth)
	}
	if profile.Headers["X-Tenant"] != "engineering" {
		t.Fatalf("unexpected headers: %#v", profile.Headers)
	}
}

func TestParseMCPProfileYAMLRejectsUnsafeCredentials(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "literal bearer token",
			content: `name: bad
url: https://mcp.example.com
auth:
  type: bearer
  token: literal-secret
`,
			want: "field token not found",
		},
		{
			name: "literal authorization header",
			content: `name: bad
url: https://mcp.example.com
headers:
  Authorization: Bearer literal-secret
`,
			want: "auth.token_env",
		},
		{
			name: "invalid token env",
			content: `name: bad
url: https://mcp.example.com
auth:
  type: bearer
  token_env: lower-case
`,
			want: "valid environment variable name",
		},
		{
			name:    "url credentials",
			content: "name: bad\nurl: https://user:secret@mcp.example.com\n",
			want:    "must not contain credentials",
		},
		{
			name:    "url query token",
			content: "name: bad\nurl: https://mcp.example.com/mcp?token=literal-secret\n",
			want:    "must not contain query parameters",
		},
		{
			name:    "punctuation-only name",
			content: "name: ...\nurl: https://mcp.example.com/mcp\n",
			want:    "name must start with a letter or number",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseMCPProfileYAML(tt.content)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestValidateAgentDefinitionFrontmatter(t *testing.T) {
	valid := `---
description: Reviews pull requests.
model: synthetic/hf:zai-org/GLM-5.2
mode: primary
permission:
  edit: allow
---

# Agent
`
	if err := ValidateAgentDefinition(valid); err != nil {
		t.Fatalf("valid agent frontmatter failed: %v", err)
	}
	if err := ValidateAgentDefinition("# Plain markdown\n"); err != nil {
		t.Fatalf("plain markdown should be accepted: %v", err)
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
