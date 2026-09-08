package definitions

import (
	"strings"
	"testing"
)

const validHMACGlobal = `name: ci-build-events
enabled: true
auth:
  type: hmac_sha256
  secret_env: CHETTER_WEBHOOK_CI_SECRET
  signature_header: X-CI-Signature
  signature_prefix: sha256=
delivery_id_header: X-Delivery-ID
event_type_header: X-Event-Type
accepted_events:
  - build.completed
action:
  type: create_task
  prompt: Investigate CI event {{ .EventType }} for {{ .Payload.repository }}
  agent: issue-triage
  timeout_sec: 900
  team_name: platform
`

func TestParseInboundWebhookYAMLValidHMAC(t *testing.T) {
	def, err := ParseInboundWebhookYAML(validHMACGlobal)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if def.Name != "ci-build-events" {
		t.Errorf("name = %q", def.Name)
	}
	if !def.Enabled {
		t.Error("enabled should default true")
	}
	if def.AuthType != InboundWebhookAuthHMAC || def.SecretEnv != "CHETTER_WEBHOOK_CI_SECRET" {
		t.Errorf("auth = %s/%s", def.AuthType, def.SecretEnv)
	}
	if def.SignatureHeader != "X-CI-Signature" || def.SignaturePrefix != "sha256=" {
		t.Errorf("signature config = %q/%q", def.SignatureHeader, def.SignaturePrefix)
	}
	if def.EventTypeHeader != "X-Event-Type" || len(def.AcceptedEvents) != 1 || def.AcceptedEvents[0] != "build.completed" {
		t.Errorf("event config = %q %v", def.EventTypeHeader, def.AcceptedEvents)
	}
	if def.ActionType != InboundWebhookActionCreateTask || def.ActionAgent != "issue-triage" || def.ActionTimeoutSec != 900 || def.ActionTeamName != "platform" {
		t.Errorf("action fields = type %q agent %q timeout %d team %q", def.ActionType, def.ActionAgent, def.ActionTimeoutSec, def.ActionTeamName)
	}
	if !strings.Contains(def.ActionPrompt, "{{ .EventType }}") {
		t.Errorf("prompt = %q", def.ActionPrompt)
	}
}

func TestParseInboundWebhookYAMLDefaults(t *testing.T) {
	yaml := `name: ci-build-events
auth:
  type: bearer
  secret_env: CHETTER_WEBHOOK_CI_TOKEN
action:
  prompt: Handle event
`
	def, err := ParseInboundWebhookYAML(yaml)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !def.Enabled {
		t.Error("enabled should default to true")
	}
	if def.SignatureHeader != "" {
		t.Errorf("bearer signature_header should be empty, got %q", def.SignatureHeader)
	}
	if def.ActionType != InboundWebhookActionCreateTask {
		t.Errorf("action type should default to create_task, got %q", def.ActionType)
	}
}

func TestParseInboundWebhookYAMLRejects(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{"unknown field", `name: x
unknown: true
auth:
  type: bearer
  secret_env: CHETTER_S
action:
  prompt: p
`, "unknown"},
		{"missing auth", "name: x\naction:\n  prompt: p\n", "auth is required"},
		{"missing action", "name: x\nauth:\n  type: bearer\n  secret_env: CHETTER_S\n", "action is required"},
		{"missing name", "auth:\n  type: bearer\n  secret_env: CHETTER_S\naction:\n  prompt: p\n", "name"},
		{"empty secret env", "name: x\nauth:\n  type: bearer\n  secret_env: \"\"\naction:\n  prompt: p\n", "secret_env"},
		{"invalid auth type", "name: x\nauth:\n  type: basic\n  secret_env: CHETTER_S\naction:\n  prompt: p\n", "auth.type"},
		{"bearer with signature header", "name: x\nauth:\n  type: bearer\n  secret_env: CHETTER_S\n  signature_header: X-Sig\naction:\n  prompt: p\n", "bearer"},
		{"missing prompt", "name: x\nauth:\n  type: bearer\n  secret_env: CHETTER_S\naction:\n  type: create_task\n", "prompt"},
		{"accepted events without header", "name: x\nauth:\n  type: bearer\n  secret_env: CHETTER_S\naccepted_events: [a]\naction:\n  prompt: p\n", "event_type_header"},
		{"duplicate accepted events", "name: x\nauth:\n  type: bearer\n  secret_env: CHETTER_S\nevent_type_header: X-T\naccepted_events: [a, a]\naction:\n  prompt: p\n", "duplicated"},
		{"bad header name", "name: x\nauth:\n  type: hmac_sha256\n  secret_env: CHETTER_S\n  signature_header: \"bad header\"\naction:\n  prompt: p\n", "signature_header"},
		{"negative timeout", "name: x\nauth:\n  type: bearer\n  secret_env: CHETTER_S\naction:\n  prompt: p\n  timeout_sec: -1\n", "timeout_sec"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseInboundWebhookYAML(tt.yaml)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateInboundWebhookScope(t *testing.T) {
	globalDef, err := ParseInboundWebhookYAML(validHMACGlobal)
	if err != nil {
		t.Fatalf("parse global: %v", err)
	}
	teamDef, err := ParseInboundWebhookYAML(strings.ReplaceAll(validHMACGlobal, "\n  team_name: platform\n", "\n"))
	if err != nil {
		t.Fatalf("parse team: %v", err)
	}
	if err := ValidateInboundWebhookScope(globalDef, "global/webhooks/inbound/ci-build-events.yaml"); err != nil {
		t.Errorf("global endpoint should pass scope validation: %v", err)
	}
	// Global endpoint without action.team_name must fail (payload can never
	// select the team).
	if err := ValidateInboundWebhookScope(teamDef, "global/webhooks/inbound/ci-build-events.yaml"); err == nil {
		t.Error("global endpoint without action.team_name should fail scope validation")
	}
	if err := ValidateInboundWebhookScope(teamDef, "groups/platform/webhooks/inbound/ci-build-events.yaml"); err != nil {
		t.Errorf("team-scoped endpoint should pass scope validation: %v", err)
	}
	// Team-scoped endpoint declaring action.team_name must fail (ownership is
	// inherited from the directory).
	if err := ValidateInboundWebhookScope(globalDef, "groups/platform/webhooks/inbound/ci-build-events.yaml"); err == nil {
		t.Error("team-scoped endpoint with action.team_name should fail scope validation")
	}
}

func TestValidateDefinitionContentInboundWebhook(t *testing.T) {
	// Filename stem must equal name.
	err := ValidateDefinitionContent(DefinitionTypeInboundWebhook,
		"global/webhooks/inbound/other-name.yaml", validHMACGlobal)
	if err == nil || !strings.Contains(err.Error(), "must match file name") {
		t.Fatalf("expected filename/name mismatch error, got %v", err)
	}
	// Valid global file passes.
	if err := ValidateDefinitionContent(DefinitionTypeInboundWebhook,
		"global/webhooks/inbound/ci-build-events.yaml", validHMACGlobal); err != nil {
		t.Fatalf("valid definition rejected: %v", err)
	}
}
