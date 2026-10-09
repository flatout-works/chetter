package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/pkg/definitions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// PromoteTriggerInput is the input for chetter_promote_trigger. It turns a
// database draft created with chetter_create_trigger into a definition file and
// a pull request against the definitions repository.
type PromoteTriggerInput struct {
	Name        string `json:"name" jsonschema:"Name of the draft trigger to promote"`
	Scope       string `json:"scope" jsonschema:"Definition scope: global, team, or repo"`
	TeamName    string `json:"team_name,omitempty" jsonschema:"Team name; required when scope is team"`
	TargetRepo  string `json:"target_repo,omitempty" jsonschema:"owner/repo the definition belongs to; required when scope is repo"`
	Path        string `json:"path,omitempty" jsonschema:"Optional definition file path override, relative to the definitions repository root"`
	Title       string `json:"title,omitempty" jsonschema:"Optional pull request title override"`
	Body        string `json:"body,omitempty" jsonschema:"Optional pull request body override"`
	SourceID    string `json:"source_id,omitempty" jsonschema:"Definitions source ID; defaults to the configured default source"`
	DraftPR     bool   `json:"draft_pr,omitempty" jsonschema:"Open the pull request as a draft"`
	DryRun      bool   `json:"dry_run,omitempty" jsonschema:"Render and validate without opening a pull request"`
	AllowSecret bool   `json:"allow_secret,omitempty" jsonschema:"Override the secret scan refusal. Use only after manual review; the pull request review remains the real gate."`
}

// PromoteTriggerOutput is the output for chetter_promote_trigger.
type PromoteTriggerOutput struct {
	TriggerName string                        `json:"trigger_name"`
	Path        string                        `json:"path"`
	Content     string                        `json:"content"`
	Proposal    *DefinitionProposalToolRecord `json:"proposal,omitempty"`
	DryRun      bool                          `json:"dry_run"`
	Warnings    []string                      `json:"warnings,omitempty"`
}

// secretPatterns are credential-shaped literals that must never be committed to
// the definitions repository. This is an accident guard, not a proof of safety:
// the pull request review remains the real gate.
var secretPatterns = []*regexp.Regexp{
	// KEY=value / TOKEN: value / password = "value" style assignments.
	regexp.MustCompile(`(?i)\b[A-Z0-9_]*(?:API_?KEY|TOKEN|SECRET|PASSWORD|PASSWD|CREDENTIAL)[A-Z0-9_]*\s*[:=]\s*["']?[^\s"',}]{8,}`),
	// Bearer and sk-/ghp_/xoxb- style literals.
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-]{16,}`),
	regexp.MustCompile(`\b(?:sk|pk|ghp|gho|ghs|github_pat|xox[baprs])[-_][A-Za-z0-9_\-]{16,}`),
	// PEM private keys.
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
}

// envVarReference matches the sanctioned way a definition names a credential:
// `api_key_env: DEEPSEEK_API_KEY`. The right-hand side is an environment
// variable NAME, not a value, so it must not be treated as a secret. Missing
// this exemption would flag every correctly written definition.
var envVarReference = regexp.MustCompile(`(?i)^\s*[A-Z0-9_]*_ENV\s*[:=]\s*["']?[A-Z][A-Z0-9_]*["']?\s*$`)

// scanRenderedTriggerForSecrets returns the offending lines, if any.
func scanRenderedTriggerForSecrets(content string) []string {
	var hits []string
	for i, line := range strings.Split(content, "\n") {
		if envVarReference.MatchString(line) {
			continue
		}
		for _, pattern := range secretPatterns {
			if pattern.MatchString(line) {
				hits = append(hits, fmt.Sprintf("line %d: %s", i+1, strings.TrimSpace(line)))
				break
			}
		}
	}
	return hits
}

// triggerDefinitionPath derives the definition file path for a scope. It
// mirrors the scanner's layout (triggers/*.yaml under each scope root) so the
// promoted file lands exactly where the sync expects it.
func triggerDefinitionPath(scope, teamName, targetRepo, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("trigger name is required")
	}
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return "", fmt.Errorf("trigger name %q cannot be used as a file name", name)
	}
	switch scope {
	case definitions.TriggerScopeGlobal:
		return "global/triggers/" + name + ".yaml", nil
	case definitions.TriggerScopeTeam:
		team := strings.TrimSpace(teamName)
		if team == "" {
			return "", fmt.Errorf("team_name is required when scope is team")
		}
		if strings.ContainsAny(team, "/\\") || strings.Contains(team, "..") {
			return "", fmt.Errorf("team name %q cannot be used as a directory name", team)
		}
		return "groups/" + team + "/triggers/" + name + ".yaml", nil
	case definitions.TriggerScopeRepo:
		repo := strings.TrimSpace(targetRepo)
		if repo == "" {
			return "", fmt.Errorf("target_repo is required when scope is repo")
		}
		owner, repoName, ok := strings.Cut(repo, "/")
		if !ok || owner == "" || repoName == "" {
			return "", fmt.Errorf("target_repo %q must be owner/repo", repo)
		}
		if strings.Contains(repoName, "/") {
			return "", fmt.Errorf("target_repo %q must be owner/repo", repo)
		}
		return "repos/" + owner + "/" + repoName + "/triggers/" + name + ".yaml", nil
	default:
		return "", fmt.Errorf("scope must be %q, %q, or %q",
			definitions.TriggerScopeGlobal, definitions.TriggerScopeTeam, definitions.TriggerScopeRepo)
	}
}

func (s *Service) promoteTriggerTool(ctx context.Context, _ *mcp.CallToolRequest, in PromoteTriggerInput) (*mcp.CallToolResult, PromoteTriggerOutput, error) {
	out, err := s.PromoteTrigger(ctx, in)
	if err != nil {
		return nil, PromoteTriggerOutput{}, err
	}
	return nil, out, nil
}

// PromoteTrigger renders a database draft as a canonical definition file and
// opens a definition proposal pull request for it.
//
// The trigger must be a draft (no source_id): promoting an already managed
// trigger is meaningless because Git is already authoritative for it. The
// rendered YAML is produced by definitions.RenderTriggerYAML, so it matches the
// canonical shape enforced by the definitions round-trip tests.
func (s *Service) PromoteTrigger(ctx context.Context, in PromoteTriggerInput) (PromoteTriggerOutput, error) {
	scope := strings.TrimSpace(in.Scope)
	if scope == "" {
		scope = definitions.TriggerScopeGlobal
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return PromoteTriggerOutput{}, fmt.Errorf("name is required")
	}
	path, err := triggerDefinitionPath(scope, in.TeamName, in.TargetRepo, name)
	if err != nil {
		return PromoteTriggerOutput{}, err
	}
	if override := strings.TrimSpace(in.Path); override != "" {
		path = strings.Trim(strings.ReplaceAll(override, "\\", "/"), "/")
	}

	record, err := s.GetTriggerByName(ctx, name)
	if err != nil {
		return PromoteTriggerOutput{}, fmt.Errorf("get trigger %q: %w", name, err)
	}
	if record.SourceID.Valid {
		return PromoteTriggerOutput{}, fmt.Errorf(
			"trigger %q is already managed by definitions source %q; edit its definition file instead of promoting it",
			name, record.SourceID.String)
	}

	// The stored agent_image is post-resolution (AGENT_IMAGE_PREFIX applied),
	// so render from the definition-level name the caller authored. Strip the
	// configured prefix when it matches; a fully qualified reference is kept.
	td := triggerDefFromRecord(triggerToStoreRecord(record), s.cfg.AgentImagePrefix)
	content, err := definitions.RenderTriggerYAML(td, scope)
	if err != nil {
		return PromoteTriggerOutput{}, fmt.Errorf("render trigger definition: %w", err)
	}

	out := PromoteTriggerOutput{TriggerName: name, Path: path, Content: content}

	if hits := scanRenderedTriggerForSecrets(content); len(hits) > 0 {
		if !in.AllowSecret {
			return PromoteTriggerOutput{}, fmt.Errorf(
				"rendered definition contains credential-shaped content and was not submitted:\n%s\n"+
					"Remove the secret from the trigger, or re-run with allow_secret after reviewing it",
				strings.Join(hits, "\n"))
		}
		out.Warnings = append(out.Warnings, "secret scan overridden with allow_secret")
	}

	if err := s.checkTriggerNameAvailable(ctx, name, path, in.SourceID); err != nil {
		return PromoteTriggerOutput{}, err
	}

	if in.DryRun {
		out.DryRun = true
		return out, nil
	}

	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "config: promote trigger " + name + " to Git-managed definitions"
	}
	body := strings.TrimSpace(in.Body)
	if body == "" {
		body = fmt.Sprintf(
			"Promotes the database draft trigger `%s` to the definitions repository.\n\n"+
				"Rendered from the live trigger row by `chetter_promote_trigger` in the canonical definition shape. "+
				"Merging this pull request makes the next definitions sync take ownership of the existing trigger row, "+
				"preserving its id and run history.",
			name)
	}
	proposal, err := s.createDefinitionProposal(ctx, CreateDefinitionProposalInput{
		SourceID:      in.SourceID,
		Title:         title,
		Body:          body,
		CommitMessage: "config: add trigger " + name,
		Files: []DefinitionProposalFileInput{
			{Path: path, Content: content},
		},
		Draft: in.DraftPR,
	})
	if err != nil {
		return PromoteTriggerOutput{}, err
	}
	out.Proposal = &proposal.Proposal
	return out, nil
}

// triggerDefFromRecord converts a stored trigger into the definition-level
// shape the renderer expects, undoing the transforms the sync applies on write.
//
// agent_image is stored fully qualified (the sync prepends AGENT_IMAGE_PREFIX),
// so the configured prefix is stripped to recover the authored short name. A
// reference that does not carry the prefix is kept as-is, which is correct for
// a definition that used a full registry reference in the first place.
func triggerDefFromRecord(rec store.TriggerRecord, agentImagePrefix string) definitions.TriggerDef {
	return definitions.TriggerDef{
		Name:        rec.Name,
		Enabled:     rec.Enabled,
		CronExpr:    rec.CronExpr,
		TriggerType: rec.TriggerType,
		TriggerCfg:  rec.TriggerConfig,
		Prompt:      rec.Prompt,
		GitURL:      rec.GitURL,
		GitRef:      rec.GitRef,
		AgentImage:  unresolveAgentImageRef(rec.AgentImage, agentImagePrefix),
		Agent:       rec.Agent,
		ProviderID:  rec.ProviderID,
		ModelID:     rec.ModelID,
		VariantID:   rec.VariantID,
		Harness:     rec.Harness,
		Skills:      rec.Skills,
		TimeoutSec:  rec.TimeoutSec,
	}
}

// unresolveAgentImageRef reverses resolveAgentImageRef for the configured
// prefix, so a promoted definition keeps the short variant name its author used.
func unresolveAgentImageRef(image, prefix string) string {
	image = strings.TrimSpace(image)
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if image == "" || prefix == "" {
		return image
	}
	return strings.TrimPrefix(image, prefix+"/")
}

// checkTriggerNameAvailable refuses a promotion that would collide with an
// existing definition file or an already-managed trigger of the same name.
// Trigger names are instance-wide identifiers, so a duplicate would replace
// another definition on sync.
func (s *Service) checkTriggerNameAvailable(ctx context.Context, name, path, sourceID string) error {
	if sourceID == "" {
		sourceID = defaultDefinitionSourceID
	}
	defs, err := s.repo.ListDefinitions(ctx, repository.ListDefinitionsParams{
		Column1:        definitions.DefinitionTypeTrigger,
		DefinitionType: definitions.DefinitionTypeTrigger,
		Column3:        "",
		SourceID:       "",
		NameFilter:     name,
	})
	if err != nil {
		return fmt.Errorf("check existing trigger definitions: %w", err)
	}
	for _, def := range defs {
		if def.SourceID != sourceID {
			continue
		}
		if def.Path == path {
			return fmt.Errorf("definition file %q already exists; update it directly instead of promoting over it", path)
		}
		return fmt.Errorf("a trigger definition named %q already exists at %q", name, def.Path)
	}
	return nil
}
