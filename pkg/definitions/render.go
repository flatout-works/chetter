package definitions

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Definition scope names accepted by RenderTriggerYAML. They mirror the
// definitionRoot scopes used by the scanner.
const (
	TriggerScopeGlobal = "global"
	TriggerScopeTeam   = "team"
	TriggerScopeRepo   = "repo"
)

// configKeys are the flat YAML keys that ParseTriggerYAML folds into the
// trigger_config JSON blob. The renderer unfolds them again so a rendered file
// uses the same authoring shape as a hand-written one. Adding a key here is the
// only way a new flat key becomes renderable, and the round-trip test fails if
// this list and ParseTriggerYAML ever disagree.
var configKeys = []string{
	"session_mode",
	"pause_reason",
	"ttl_hours",
	"repo",
	"event",
	"match_labels",
}

// TriggerSchemaCommentPrefix returns the relative path prefix used in the
// `# yaml-language-server: $schema=` header comment for a definition living in
// the given scope. The depth differs per scope because the comment is relative
// to the file's own directory:
//
//	global/triggers/x.yaml                -> ../../../chetter/schemas/...
//	groups/<team>/triggers/x.yaml         -> ../../../../chetter/schemas/...
//	repos/<owner>/<repo>/triggers/x.yaml  -> ../../../../../chetter/schemas/...
//
// The parser discards comments entirely, so nothing in the parsed definition
// records this and the renderer must recompute it from the target scope.
func TriggerSchemaCommentPrefix(scope string) string {
	switch scope {
	case TriggerScopeTeam:
		return "../../../../"
	case TriggerScopeRepo:
		return "../../../../../"
	default:
		return "../../../"
	}
}

// TriggerSchemaComment returns the full header comment line for a scope.
func TriggerSchemaComment(scope string) string {
	return "# yaml-language-server: $schema=" + TriggerSchemaCommentPrefix(scope) + "chetter/schemas/trigger.schema.json"
}

// RenderTriggerYAML renders a TriggerDef back to canonical definition YAML.
//
// It inverts the transforms ParseTriggerYAML applies, so
// RenderTriggerYAML(ParseTriggerYAML(content), scope) describes the same
// trigger as content, in this repository's canonical field order:
//
//   - the flat keys in configKeys are unfolded out of the trigger_config JSON
//     blob back into top-level keys;
//   - trigger_type is omitted when it is the "cron" default;
//   - the per-scope $schema header comment is recomputed from scope.
//
// Free-text comments in the source file are not recoverable and are replaced by
// a generated header. Rendering is deterministic: the same TriggerDef always
// produces the same bytes, which is the property the round-trip test asserts.
func RenderTriggerYAML(td TriggerDef, scope string) (string, error) {
	if strings.TrimSpace(td.Name) == "" {
		return "", fmt.Errorf("trigger name is required")
	}
	flat, remaining, err := unfoldTriggerConfig(td.TriggerCfg)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	b.WriteString(TriggerSchemaComment(scope))
	b.WriteString("\n")
	b.WriteString("# Trigger: " + td.Name + "\n")
	b.WriteString("\n")

	// Canonical field order. Each field is written only when it carries a
	// value, except name and enabled which every definition states explicitly.
	writeScalar(&b, "name", td.Name)
	writeScalar(&b, "enabled", fmt.Sprintf("%t", td.Enabled))
	if td.TriggerType != "" && td.TriggerType != "cron" {
		writeScalar(&b, "trigger_type", td.TriggerType)
	}
	if td.CronExpr != "" {
		// cron expressions are always quoted in the repository's style.
		b.WriteString("cron_expr: " + strconvQuote(td.CronExpr) + "\n")
	}
	writeScalar(&b, "repo", flat["repo"])
	writeScalar(&b, "event", flat["event"])
	if labels := splitLabels(flat["match_labels"]); len(labels) > 0 {
		b.WriteString("match_labels:\n")
		for _, label := range labels {
			b.WriteString("  - " + label + "\n")
		}
	}
	writeScalar(&b, "git_url", td.GitURL)
	writeScalar(&b, "git_ref", td.GitRef)
	writeScalar(&b, "agent_image", td.AgentImage)
	writeScalar(&b, "harness", td.Harness)
	writeScalar(&b, "agent", td.Agent)
	writeScalar(&b, "provider_id", td.ProviderID)
	writeScalar(&b, "model_id", td.ModelID)
	writeScalar(&b, "variant_id", td.VariantID)
	if len(td.Skills) > 0 {
		b.WriteString("skills:\n")
		for _, skill := range td.Skills {
			b.WriteString("  - " + skill + "\n")
		}
	}
	if td.TimeoutSec > 0 {
		writeScalar(&b, "timeout_sec", fmt.Sprintf("%d", td.TimeoutSec))
	}
	writeScalar(&b, "session_mode", flat["session_mode"])
	writeScalar(&b, "pause_reason", flat["pause_reason"])
	writeScalar(&b, "ttl_hours", flat["ttl_hours"])
	if remaining != "" {
		writeScalar(&b, "trigger_config", remaining)
	}
	// Only emitted when set: `adopt` is an escape hatch, not a routine field.
	if td.Adopt {
		b.WriteString("adopt: true\n")
	}
	if td.Prompt != "" {
		b.WriteString("prompt: " + promptScalarHeader(td.Prompt) + "\n")
		b.WriteString(renderPromptBody(td.Prompt))
	}
	return b.String(), nil
}

// writeScalar writes "key: value" when value is non-empty.
func writeScalar(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	b.WriteString(key + ": " + value + "\n")
}

func strconvQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// splitLabels parses the JSON array stored for match_labels back into a list.
func splitLabels(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// blockScalarSafe reports whether prompt can be emitted as a `|-` literal
// block without changing its parsed value. The failure modes are a leading
// blank line, any line beginning with whitespace (which needs an explicit
// indentation indicator), and trailing newlines (which `|-` strips).
func blockScalarSafe(prompt string) bool {
	if prompt == "" {
		return false
	}
	if strings.TrimRight(prompt, "\n") != prompt {
		return false
	}
	lines := strings.Split(prompt, "\n")
	if lines[0] == "" {
		return false
	}
	for _, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			return false
		}
	}
	return true
}

// promptScalarHeader returns the YAML scalar header for a prompt value.
func promptScalarHeader(prompt string) string {
	if blockScalarSafe(prompt) {
		return "|-"
	}
	encoded, err := json.Marshal(prompt)
	if err != nil {
		// json.Marshal of a string cannot fail; fall back defensively.
		return strconvQuote(prompt)
	}
	return string(encoded)
}

// renderPromptBody indents a prompt for a `|-` block scalar, or returns no body
// when the prompt is rendered inline as a quoted scalar.
func renderPromptBody(prompt string) string {
	if !blockScalarSafe(prompt) {
		return ""
	}
	var b strings.Builder
	for _, line := range strings.Split(prompt, "\n") {
		if line == "" {
			b.WriteString("\n")
			continue
		}
		b.WriteString("  " + line + "\n")
	}
	return b.String()
}

// unfoldTriggerConfig splits a trigger_config JSON blob into the flat keys that
// ParseTriggerYAML folded into it, plus the remaining JSON (empty when nothing
// is left). Flat keys are returned pre-rendered as they should appear in YAML.
func unfoldTriggerConfig(triggerCfg string) (map[string]string, string, error) {
	flat := map[string]string{}
	triggerCfg = strings.TrimSpace(triggerCfg)
	if triggerCfg == "" || triggerCfg == "{}" {
		return flat, "", nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(triggerCfg)))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, "", fmt.Errorf("parse trigger_config JSON: %w", err)
	}
	for _, key := range configKeys {
		v, ok := raw[key]
		if !ok {
			continue
		}
		delete(raw, key)
		switch key {
		case "match_labels":
			labels, err := toStringSlice(v)
			if err != nil {
				return nil, "", fmt.Errorf("trigger_config.match_labels: %w", err)
			}
			if len(labels) == 0 {
				continue
			}
			encoded, err := json.Marshal(labels)
			if err != nil {
				return nil, "", fmt.Errorf("encode match_labels: %w", err)
			}
			flat[key] = string(encoded)
		case "ttl_hours":
			if n, ok := v.(json.Number); ok {
				flat[key] = n.String()
			}
		default:
			if s, ok := v.(string); ok && s != "" {
				flat[key] = s
			}
		}
	}
	if len(raw) == 0 {
		return flat, "", nil
	}
	// json.Marshal sorts map keys, so leftover config is deterministic.
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, "", fmt.Errorf("encode trigger_config: %w", err)
	}
	return flat, string(encoded), nil
}

func toStringSlice(v any) ([]string, error) {
	switch value := v.(type) {
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("expected strings")
			}
			out = append(out, s)
		}
		return out, nil
	case string:
		return []string{value}, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("expected a string or list of strings")
	}
}
