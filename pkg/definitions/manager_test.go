package definitions

import (
	"encoding/json"
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
	writeFile(t, root, "mcp-profiles/chetter-orchestration.yaml", "name: chetter-orchestration\nurl: http://chetter-mcp:8080/mcp\n")

	m := New("", "", root)
	defs, err := m.ScanDefinitions()
	if err != nil {
		t.Fatalf("scan definitions: %v", err)
	}
	if len(defs) != 6 {
		t.Fatalf("expected 6 definitions, got %d: %#v", len(defs), defs)
	}
	assertDefinition(t, defs, DefinitionTypeAgent, "pr-reviewer", "agents/pr-reviewer.md")
	assertDefinition(t, defs, DefinitionTypeSkill, "chetter", "skills/chetter/SKILL.md")
	assertDefinition(t, defs, DefinitionTypeSkill, "flat", "skills/flat.md")
	assertDefinition(t, defs, DefinitionTypeTrigger, "nightly", "triggers/nightly.yaml")
	assertDefinition(t, defs, DefinitionTypeTaskTemplate, "improve", "task-templates/improve.md")
	assertDefinition(t, defs, DefinitionTypeMCPProfile, "chetter-orchestration", "mcp-profiles/chetter-orchestration.yaml")
	for _, def := range defs {
		if def.ContentHash == "" {
			t.Fatalf("definition %s/%s has empty hash", def.Type, def.Name)
		}
	}
}

func TestParseTriggerYAMLIncludesMCPProfiles(t *testing.T) {
	trigger, err := ParseTriggerYAML(`
name: pr-review
trigger_type: pr_review
repo: flatout-works/chetter
skills:
  - pr-review-workflow
mcp_profiles:
  - chetter-orchestration
`)
	if err != nil {
		t.Fatalf("ParseTriggerYAML failed: %v", err)
	}
	if len(trigger.MCPProfiles) != 1 || trigger.MCPProfiles[0] != "chetter-orchestration" {
		t.Fatalf("MCPProfiles = %#v, want [chetter-orchestration]", trigger.MCPProfiles)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(trigger.TriggerCfg), &cfg); err != nil {
		t.Fatalf("trigger_config is not valid JSON: %v", err)
	}
	if cfg["repo"] != "flatout-works/chetter" {
		t.Fatalf("trigger_config repo = %v, want flatout-works/chetter", cfg["repo"])
	}
}

func TestParseTriggerYAMLHonorsDisabledTriggers(t *testing.T) {
	trigger, err := ParseTriggerYAML(`
name: disabled-review
enabled: false
trigger_type: pr_review
repo: flatout-works/chetter
`)
	if err != nil {
		t.Fatalf("ParseTriggerYAML failed: %v", err)
	}
	if trigger.Enabled {
		t.Fatal("enabled=false trigger parsed as enabled")
	}
}

func TestParseMCPProfileYAML(t *testing.T) {
	profile, err := ParseMCPProfileYAML(`
name: chetter-orchestration
transport: http
url: http://chetter-mcp:8080/mcp
auth:
  type: bearer
  token: ${env:CHETTER_MCP_AUTH_TOKEN}
tool_allowlist:
  - chetter_submit_task
  - chetter_task_status
`)
	if err != nil {
		t.Fatalf("ParseMCPProfileYAML failed: %v", err)
	}
	if profile.Name != "chetter-orchestration" || profile.Transport != "http" || profile.URL != "http://chetter-mcp:8080/mcp" {
		t.Fatalf("unexpected profile: %+v", profile)
	}
	if profile.Headers["Authorization"] != "Bearer ${env:CHETTER_MCP_AUTH_TOKEN}" {
		t.Fatalf("Authorization header = %q", profile.Headers["Authorization"])
	}
	if len(profile.ToolAllowlist) != 2 || profile.ToolAllowlist[1] != "chetter_task_status" {
		t.Fatalf("ToolAllowlist = %#v", profile.ToolAllowlist)
	}
}

func TestParseMCPProfileYAMLCanonicalizesAuthorizationHeader(t *testing.T) {
	profile, err := ParseMCPProfileYAML(`
name: chetter-orchestration
url: http://chetter-mcp:8080/mcp
headers:
  authorization: Bearer explicit-token
auth:
  type: bearer
  token: ${env:SHOULD_NOT_BE_ADDED}
`)
	if err != nil {
		t.Fatalf("ParseMCPProfileYAML failed: %v", err)
	}
	if _, ok := profile.Headers["authorization"]; ok {
		t.Fatalf("lowercase authorization header should be canonicalized: %#v", profile.Headers)
	}
	if got := profile.Headers["Authorization"]; got != "Bearer explicit-token" {
		t.Fatalf("Authorization header = %q, want explicit token", got)
	}
	if len(profile.Headers) != 1 {
		t.Fatalf("expected one header, got %#v", profile.Headers)
	}
}

func TestParseMCPProfileYAMLRejectsDuplicateHeaderCasing(t *testing.T) {
	cases := []string{
		`
name: chetter-orchestration
url: http://chetter-mcp:8080/mcp
headers:
  X-Chetter: one
  x-chetter: two
`,
		`
name: chetter-orchestration
url: http://chetter-mcp:8080/mcp
headers:
  Authorization: Bearer one
  authorization: Bearer two
`,
	}
	for _, content := range cases {
		_, err := ParseMCPProfileYAML(content)
		if err == nil {
			t.Fatal("expected duplicate header casing error")
		}
	}
}

func TestParseMCPProfileYAMLRejectsReservedNames(t *testing.T) {
	for _, name := range []string{"chetter", "CheTTeR", "runner-bridge", "RUnNeR-BrIdGe"} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseMCPProfileYAML("name: " + name + "\nurl: http://chetter-mcp:8080/mcp\n")
			if err == nil {
				t.Fatal("expected reserved name error")
			}
		})
	}
}

func TestExampleReviewOrchestrationDefinitionsParse(t *testing.T) {
	root := filepath.Join("..", "..", "examples", "config-repo")
	profileData, err := os.ReadFile(filepath.Join(root, "mcp-profiles", "chetter-orchestration.yaml"))
	if err != nil {
		t.Fatalf("read example mcp profile: %v", err)
	}
	profile, err := ParseMCPProfileYAML(string(profileData))
	if err != nil {
		t.Fatalf("parse example mcp profile: %v", err)
	}
	if profile.Name != "chetter-orchestration" {
		t.Fatalf("profile name = %q", profile.Name)
	}
	if profile.Headers["Authorization"] == "" {
		t.Fatalf("example profile is missing Authorization header: %#v", profile.Headers)
	}
	if len(profile.ToolAllowlist) != 0 {
		t.Fatalf("credentialed example profile must not declare tool_allowlist: %#v", profile.ToolAllowlist)
	}

	triggerData, err := os.ReadFile(filepath.Join(root, "triggers", "chetter-pr-review-orchestrator.yaml"))
	if err != nil {
		t.Fatalf("read example trigger: %v", err)
	}
	trigger, err := ParseTriggerYAML(string(triggerData))
	if err != nil {
		t.Fatalf("parse example trigger: %v", err)
	}
	if trigger.TriggerType != "pr_review" || trigger.Agent != "review-orchestrator" {
		t.Fatalf("unexpected trigger: %+v", trigger)
	}
	if trigger.Enabled {
		t.Fatal("example orchestrator trigger should remain disabled by default")
	}
	if !containsString(trigger.Skills, "pr-review-workflow") {
		t.Fatalf("trigger skills = %#v, want pr-review-workflow", trigger.Skills)
	}
	if !containsString(trigger.MCPProfiles, "chetter-orchestration") {
		t.Fatalf("trigger mcp_profiles = %#v, want chetter-orchestration", trigger.MCPProfiles)
	}

	orchestratorData, err := os.ReadFile(filepath.Join(root, "agents", "review-orchestrator.md"))
	if err != nil {
		t.Fatalf("read example orchestrator: %v", err)
	}
	orchestrator := string(orchestratorData)
	childSection := sectionBetween(orchestrator, "3. Submit two child tasks", "4. Poll both child tasks")
	childEnvLine := lineContaining(childSection, "environment values")
	if !strings.Contains(childEnvLine, "CHETTER_PARENT_TASK_ID") || !strings.Contains(childEnvLine, "CHETTER_GITHUB_AUTH_MODE") {
		t.Fatalf("reviewer child env line must include parent read auth fields:\n%s", childEnvLine)
	}
	if strings.Contains(childEnvLine, "GITHUB_TOKEN") {
		t.Fatalf("reviewer child env line must not pass literal GitHub tokens:\n%s", childEnvLine)
	}
	if !strings.Contains(childSection, "`CHETTER_GITHUB_AUTH_MODE` must be set to `read`") ||
		!strings.Contains(childSection, "must not inherit GitHub write authorization") {
		t.Fatalf("reviewer child section should require read-only GitHub inheritance:\n%s", childSection)
	}
	synthSection := sectionBetween(orchestrator, "6. Submit the synthesizer task", "7. Poll the synthesizer")
	if !strings.Contains(synthSection, "`task_export_files`") ||
		!strings.Contains(synthSection, "`extra_files`") ||
		!strings.Contains(synthSection, "reviews/standard.md") ||
		!strings.Contains(synthSection, "reviews/adversarial.md") {
		t.Fatalf("synthesizer section should inject child exports via server-side files:\n%s", synthSection)
	}
	if strings.Contains(synthSection, "containing the exported child transcripts") {
		t.Fatalf("synthesizer section should not ask orchestrator to read transcript contents:\n%s", synthSection)
	}
	if strings.Contains(synthSection, "`mcp_profiles`: `[\"chetter-orchestration\"]`") ||
		strings.Contains(synthSection, "`CHETTER_GITHUB_AUTH_MODE` must be set to `write`") {
		t.Fatalf("synthesizer section must not grant Chetter MCP or write auth:\n%s", synthSection)
	}
	if !strings.Contains(synthSection, "do not set `mcp_profiles`, `CHETTER_PARENT_TASK_ID`, or `CHETTER_GITHUB_AUTH_MODE`") {
		t.Fatalf("synthesizer section should explicitly forbid inherited credentials:\n%s", synthSection)
	}
	exportSection := sectionBetween(orchestrator, "5. Prepare synthesizer inputs", "6. Submit the synthesizer task")
	if !strings.Contains(exportSection, "Do not call `chetter_task_export`") {
		t.Fatalf("orchestrator should not read child task exports:\n%s", exportSection)
	}
	postSection := sectionBetween(orchestrator, "8. Verify the PR head again", "## Rules")
	if !strings.Contains(postSection, "`body_task_export_id`") ||
		!strings.Contains(postSection, "without returning it to you") {
		t.Fatalf("orchestrator should post synthesizer export without reading it:\n%s", postSection)
	}

	synthData, err := os.ReadFile(filepath.Join(root, "agents", "review-synthesizer.md"))
	if err != nil {
		t.Fatalf("read example synthesizer: %v", err)
	}
	synthesizer := string(synthData)
	if !strings.Contains(synthesizer, "must not have Chetter MCP profiles or GitHub write credentials") ||
		!strings.Contains(synthesizer, "Do not call Chetter MCP tools") {
		t.Fatalf("synthesizer should run without privileged MCP credentials:\n%s", synthesizer)
	}
}

func sectionBetween(content, start, end string) string {
	startIdx := strings.Index(content, start)
	if startIdx < 0 {
		return ""
	}
	content = content[startIdx:]
	endIdx := strings.Index(content, end)
	if endIdx < 0 {
		return content
	}
	return content[:endIdx]
}

func lineContaining(content, needle string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
