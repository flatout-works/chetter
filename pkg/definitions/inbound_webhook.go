package definitions

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// InboundWebhookDef is the validated model of an inbound webhook endpoint
// definition from chetter-config (issue #120, epic #253 Phase 2). Inbound
// endpoints let external systems submit authenticated events that Chetter
// durably turns into one configured action (create_task in the first
// version). Definitions live under:
//
//	global/webhooks/inbound/*.yaml
//	groups/<team>/webhooks/inbound/*.yaml
//
// The YAML is strict (unknown fields are rejected) and secrets are only ever
// referenced by environment variable name (auth.secret_env) — never inlined.
type InboundWebhookDef struct {
	Name             string
	Enabled          bool
	AuthType         string // "hmac_sha256" or "bearer"
	SecretEnv        string
	SignatureHeader  string
	SignaturePrefix  string
	DeliveryIDHeader string
	EventTypeHeader  string
	AcceptedEvents   []string
	ActionType       string // "create_task"
	ActionPrompt     string
	ActionAgent      string
	ActionTimeoutSec int
	// ActionTeamName is required for global-scope endpoints and must be empty
	// for team-scope endpoints (the team is inherited from the directory).
	ActionTeamName string
}

const (
	InboundWebhookAuthHMAC   = "hmac_sha256"
	InboundWebhookAuthBearer = "bearer"

	InboundWebhookActionCreateTask = "create_task"

	// DefaultInboundSignatureHeader is used for hmac_sha256 endpoints that do
	// not declare auth.signature_header.
	DefaultInboundSignatureHeader = "X-Chetter-Signature"
)

type rawInboundWebhookYAML struct {
	Name             string                       `yaml:"name"`
	Enabled          *bool                        `yaml:"enabled"`
	Auth             *rawInboundWebhookAuthYAML   `yaml:"auth"`
	DeliveryIDHeader string                       `yaml:"delivery_id_header"`
	EventTypeHeader  string                       `yaml:"event_type_header"`
	AcceptedEvents   []string                     `yaml:"accepted_events"`
	Action           *rawInboundWebhookActionYAML `yaml:"action"`
}

type rawInboundWebhookAuthYAML struct {
	Type            string `yaml:"type"`
	SecretEnv       string `yaml:"secret_env"`
	SignatureHeader string `yaml:"signature_header"`
	SignaturePrefix string `yaml:"signature_prefix"`
}

type rawInboundWebhookActionYAML struct {
	Type       string `yaml:"type"`
	Prompt     string `yaml:"prompt"`
	Agent      string `yaml:"agent"`
	TimeoutSec int    `yaml:"timeout_sec"`
	TeamName   string `yaml:"team_name"`
}

var (
	httpHeaderNameRE    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]*$`)
	signaturePrefixRE   = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{0,64}$`)
	validEndpointNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// ParseInboundWebhookYAML validates one inbound endpoint definition and
// returns its parsed model. Validation is strict: unknown YAML fields fail,
// auth/action sections are required, header and environment references are
// syntax-checked, and hmac_sha256/bearer semantics are enforced.
func ParseInboundWebhookYAML(content string) (InboundWebhookDef, error) {
	var raw rawInboundWebhookYAML
	dec := yaml.NewDecoder(bytes.NewReader([]byte(content)))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return InboundWebhookDef{}, fmt.Errorf("parse inbound webhook yaml: %w", err)
	}

	name := strings.TrimSpace(raw.Name)
	if !validEndpointNameRE.MatchString(name) {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook name must start with a letter or number, contain only letters, numbers, dot, underscore, or dash, and be at most 128 characters")
	}
	enabled := true
	if raw.Enabled != nil {
		enabled = *raw.Enabled
	}
	if raw.Auth == nil {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: auth is required", name)
	}
	authType := strings.ToLower(strings.TrimSpace(raw.Auth.Type))
	if authType != InboundWebhookAuthHMAC && authType != InboundWebhookAuthBearer {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: auth.type must be hmac_sha256 or bearer", name)
	}
	secretEnv := strings.TrimSpace(raw.Auth.SecretEnv)
	if !validEnvName(secretEnv) {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: auth.secret_env must be a valid environment variable name", name)
	}
	signatureHeader := strings.TrimSpace(raw.Auth.SignatureHeader)
	signaturePrefix := strings.TrimSpace(raw.Auth.SignaturePrefix)
	if authType == InboundWebhookAuthBearer {
		if signatureHeader != "" || signaturePrefix != "" {
			return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: bearer auth does not accept signature_header or signature_prefix", name)
		}
	} else {
		if signatureHeader == "" {
			signatureHeader = DefaultInboundSignatureHeader
		}
		if !httpHeaderNameRE.MatchString(signatureHeader) {
			return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: auth.signature_header must be a valid HTTP header name", name)
		}
		if signaturePrefix != "" && !signaturePrefixRE.MatchString(signaturePrefix) {
			return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: auth.signature_prefix must be an ASCII prefix of at most 64 characters", name)
		}
	}

	deliveryIDHeader := strings.TrimSpace(raw.DeliveryIDHeader)
	if deliveryIDHeader != "" && !httpHeaderNameRE.MatchString(deliveryIDHeader) {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: delivery_id_header must be a valid HTTP header name", name)
	}
	eventTypeHeader := strings.TrimSpace(raw.EventTypeHeader)
	if eventTypeHeader != "" && !httpHeaderNameRE.MatchString(eventTypeHeader) {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: event_type_header must be a valid HTTP header name", name)
	}
	if len(raw.AcceptedEvents) > 0 && eventTypeHeader == "" {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: accepted_events requires event_type_header so the event can be classified", name)
	}
	acceptedEvents := make([]string, 0, len(raw.AcceptedEvents))
	seen := map[string]struct{}{}
	for _, event := range raw.AcceptedEvents {
		event = strings.TrimSpace(event)
		if event == "" || len(event) > 128 {
			return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: accepted_events entries must be non-empty and at most 128 characters", name)
		}
		if _, ok := seen[event]; ok {
			return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: accepted_events entry %q is duplicated", name, event)
		}
		seen[event] = struct{}{}
		acceptedEvents = append(acceptedEvents, event)
	}

	if raw.Action == nil {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: action is required", name)
	}
	actionType := strings.ToLower(strings.TrimSpace(raw.Action.Type))
	if actionType == "" {
		actionType = InboundWebhookActionCreateTask
	}
	if actionType != InboundWebhookActionCreateTask {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: action.type must be create_task", name)
	}
	prompt := strings.TrimSpace(raw.Action.Prompt)
	if prompt == "" {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: action.prompt is required", name)
	}
	agent := strings.TrimSpace(raw.Action.Agent)
	if len(agent) > 128 {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: action.agent must be at most 128 characters", name)
	}
	if raw.Action.TimeoutSec < 0 {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: action.timeout_sec must be greater than or equal to 0", name)
	}
	teamName := strings.TrimSpace(raw.Action.TeamName)
	if len(teamName) > 128 {
		return InboundWebhookDef{}, fmt.Errorf("inbound webhook %q: action.team_name must be at most 128 characters", name)
	}

	return InboundWebhookDef{
		Name:             name,
		Enabled:          enabled,
		AuthType:         authType,
		SecretEnv:        secretEnv,
		SignatureHeader:  signatureHeader,
		SignaturePrefix:  signaturePrefix,
		DeliveryIDHeader: deliveryIDHeader,
		EventTypeHeader:  eventTypeHeader,
		AcceptedEvents:   acceptedEvents,
		ActionType:       actionType,
		ActionPrompt:     prompt,
		ActionAgent:      agent,
		ActionTimeoutSec: raw.Action.TimeoutSec,
		ActionTeamName:   teamName,
	}, nil
}

// ValidateInboundWebhookScope enforces the scope/ownership rules of issue #120
// at definition-scan time: a global endpoint must declare the fixed action
// team (action.team_name), while a team-scoped endpoint inherits its team from
// the groups/<team>/ directory and must not declare action.team_name. Payload
// data can never select the target team.
func ValidateInboundWebhookScope(def InboundWebhookDef, relPath string) error {
	switch {
	case strings.HasPrefix(relPath, "global/"):
		if def.ActionTeamName == "" {
			return fmt.Errorf("inbound webhook %q: global endpoints must declare action.team_name (payload data can never select the target team)", def.Name)
		}
	case strings.HasPrefix(relPath, "groups/"):
		if def.ActionTeamName != "" {
			return fmt.Errorf("inbound webhook %q: team-scoped endpoints inherit their team from the groups/<team>/ directory and must not declare action.team_name", def.Name)
		}
	default:
		return fmt.Errorf("inbound webhook %q: unsupported definition path %q", def.Name, relPath)
	}
	return nil
}
