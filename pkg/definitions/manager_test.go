package definitions

import (
	"os"
	"path/filepath"
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
