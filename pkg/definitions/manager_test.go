package definitions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanDefinitions(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "agents/pr-reviewer.md", "---\nidentity: primary-bot\n---\n# PR reviewer\n")
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
