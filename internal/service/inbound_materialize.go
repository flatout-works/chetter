package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/pkg/definitions"
)

// inboundEndpointSpec is a parsed inbound endpoint definition bound to a
// fixed runtime team, ready for materialization.
type inboundEndpointSpec struct {
	def    definitions.InboundWebhookDef
	source definitions.Definition
	scope  string
	teamID string
}

// materializeInboundWebhookEndpoints reconciles the webhook_endpoints table
// with the Git-managed inbound webhook definitions discovered by a definition
// sync (issue #120). Endpoint identity is stable: the opaque public_id and
// row id are kept while a definition keeps its source path; deleting the file
// removes the endpoint. This runs after the definitions transaction commits
// so definitions rows are visible and sync errors are recorded consistently.
func (s *Service) materializeInboundWebhookEndpoints(ctx context.Context, defs []definitions.Definition, definitionTeamIDs map[string]string, now time.Time) error {
	if s.rawDB == nil {
		return fmt.Errorf("database not available")
	}
	specs, err := s.inboundEndpointSpecs(ctx, defs, definitionTeamIDs)
	if err != nil {
		return err
	}
	desired := make(map[string]inboundEndpointSpec, len(specs))
	for _, spec := range specs {
		desired[spec.source.Path] = spec
	}

	existing, err := s.listInboundEndpointRows(ctx)
	if err != nil {
		return err
	}

	for _, existingRow := range existing {
		spec, keep := desired[existingRow.sourcePath]
		if !keep {
			if _, err := s.rawDB.ExecContext(ctx, "DELETE FROM webhook_endpoints WHERE id = "+s.inboundPh(1), existingRow.id); err != nil {
				return fmt.Errorf("delete removed inbound endpoint %s: %w", existingRow.sourcePath, err)
			}
			s.auditAsync(ctx, AuditEventParams{
				EventType:  "inbound_endpoint_removed",
				SourceType: "definitions",
				SourceID:   existingRow.sourcePath,
				TargetType: "webhook_endpoint",
				TargetID:   existingRow.id,
				Detail:     fmt.Sprintf("inbound endpoint %s removed (definition deleted)", existingRow.sourcePath),
			})
			slog.Info("inbound endpoint removed by definition sync", "source_path", existingRow.sourcePath, "public_id", existingRow.publicID)
			continue
		}
		if err := s.updateInboundEndpointRow(ctx, existingRow.id, spec, now); err != nil {
			return err
		}
		s.auditAsync(ctx, AuditEventParams{
			EventType:  "inbound_endpoint_updated",
			SourceType: "definitions",
			SourceID:   spec.source.Path,
			TargetType: "webhook_endpoint",
			TargetID:   existingRow.id,
			Detail:     fmt.Sprintf("inbound endpoint %s updated by definition sync", spec.def.Name),
		})
		slog.Info("inbound endpoint updated by definition sync", "source_path", spec.source.Path, "public_id", existingRow.publicID)
		delete(desired, spec.source.Path)
	}

	for path, spec := range desired {
		id, err := randomID("inb")
		if err != nil {
			return fmt.Errorf("generate inbound endpoint id: %w", err)
		}
		publicID, err := randomID("inb")
		if err != nil {
			return fmt.Errorf("generate inbound endpoint public id: %w", err)
		}
		if err := s.insertInboundEndpointRow(ctx, id, publicID, spec, now); err != nil {
			return err
		}
		s.auditAsync(ctx, AuditEventParams{
			EventType:  "inbound_endpoint_created",
			SourceType: "definitions",
			SourceID:   path,
			TargetType: "webhook_endpoint",
			TargetID:   id,
			Detail:     fmt.Sprintf("inbound endpoint %s materialized at /hooks/inbound/%s", spec.def.Name, publicID),
		})
		slog.Info("inbound endpoint materialized by definition sync", "source_path", path, "public_id", publicID)
	}
	return nil
}

// inboundEndpointSpecs parses each inbound webhook definition and resolves its
// fixed runtime team. Payload data never selects the team: global endpoints
// use action.team_name and team-scoped endpoints inherit the groups directory
// team resolved from the definitions scan.
func (s *Service) inboundEndpointSpecs(ctx context.Context, defs []definitions.Definition, definitionTeamIDs map[string]string) ([]inboundEndpointSpec, error) {
	var specs []inboundEndpointSpec
	for _, def := range defs {
		if def.Type != definitions.DefinitionTypeInboundWebhook {
			continue
		}
		parsed, err := definitions.ParseInboundWebhookYAML(def.Content)
		if err != nil {
			return nil, fmt.Errorf("parse inbound webhook definition %s: %w", def.Path, err)
		}
		spec := inboundEndpointSpec{def: parsed, source: def}
		switch def.Scope {
		case definitions.DefinitionScopeGlobal:
			spec.scope = definitionScopeGlobal
			team, err := s.repo.GetTeamByName(ctx, parsed.ActionTeamName)
			if err != nil {
				return nil, fmt.Errorf("inbound webhook definition %s: resolve action team %q: %w", def.Path, parsed.ActionTeamName, err)
			}
			spec.teamID = team.ID
		case definitions.DefinitionScopeTeam:
			spec.scope = definitionScopeTeam
			teamID, ok := definitionTeamIDs[def.TeamName]
			if !ok || teamID == "" {
				return nil, fmt.Errorf("inbound webhook definition %s: group %q does not match an existing team", def.Path, def.TeamName)
			}
			spec.teamID = teamID
		default:
			return nil, fmt.Errorf("inbound webhook definition %s: repo-scoped inbound endpoints are not supported", def.Path)
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

type inboundEndpointRowKey struct {
	id         string
	publicID   string
	sourcePath string
}

func (s *Service) listInboundEndpointRows(ctx context.Context) ([]inboundEndpointRowKey, error) {
	rows, err := s.rawDB.QueryContext(ctx, "SELECT id, public_id, source_path FROM webhook_endpoints")
	if err != nil {
		return nil, fmt.Errorf("list inbound endpoint rows: %w", err)
	}
	defer rows.Close()
	var out []inboundEndpointRowKey
	for rows.Next() {
		var row inboundEndpointRowKey
		if err := rows.Scan(&row.id, &row.publicID, &row.sourcePath); err != nil {
			return nil, fmt.Errorf("scan inbound endpoint row: %w", err)
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

const inboundEndpointInsertColumns = `(id, public_id, name, scope, team_id, source_path, enabled, auth_type, secret_env,
	signature_header, signature_prefix, delivery_id_header, event_type_header,
	accepted_events, action_type, action_prompt, action_agent, action_timeout_sec,
	created_at, updated_at)`

func (s *Service) insertInboundEndpointRow(ctx context.Context, id, publicID string, spec inboundEndpointSpec, now time.Time) error {
	acceptedEvents, err := acceptedEventsJSON(spec.def.AcceptedEvents)
	if err != nil {
		return err
	}
	query := "INSERT INTO webhook_endpoints " + inboundEndpointInsertColumns +
		" VALUES (" + s.placeholderList(20) + ")"
	_, err = s.rawDB.ExecContext(ctx, query,
		id, publicID, spec.def.Name, spec.scope, nilStringOrNil(spec.teamID), spec.source.Path,
		spec.def.Enabled, spec.def.AuthType, spec.def.SecretEnv,
		nullableString(spec.def.SignatureHeader), nullableString(spec.def.SignaturePrefix),
		nullableString(spec.def.DeliveryIDHeader), nullableString(spec.def.EventTypeHeader),
		acceptedEvents, spec.def.ActionType, spec.def.ActionPrompt,
		nullableString(spec.def.ActionAgent), nullableInt(spec.def.ActionTimeoutSec), now, now)
	if err != nil {
		return fmt.Errorf("insert inbound endpoint %s: %w", spec.source.Path, err)
	}
	return nil
}

func (s *Service) updateInboundEndpointRow(ctx context.Context, id string, spec inboundEndpointSpec, now time.Time) error {
	acceptedEvents, err := acceptedEventsJSON(spec.def.AcceptedEvents)
	if err != nil {
		return err
	}
	// Two placeholder sets so a single UPDATE works for both dialects.
	ph := func(offset int) string {
		if s.dialect == store.DialectPostgres {
			return fmt.Sprintf("$%d", offset+1)
		}
		return "?"
	}
	query := `UPDATE webhook_endpoints SET
		name = ` + ph(0) + `, scope = ` + ph(1) + `, team_id = ` + ph(2) + `, source_path = ` + ph(3) + `, enabled = ` + ph(4) + `, auth_type = ` + ph(5) + `,
		secret_env = ` + ph(6) + `, signature_header = ` + ph(7) + `, signature_prefix = ` + ph(8) + `, delivery_id_header = ` + ph(9) + `,
		event_type_header = ` + ph(10) + `, accepted_events = ` + ph(11) + `, action_type = ` + ph(12) + `, action_prompt = ` + ph(13) + `,
		action_agent = ` + ph(14) + `, action_timeout_sec = ` + ph(15) + `, updated_at = ` + ph(16) + ` WHERE id = ` + ph(17)
	_, err = s.rawDB.ExecContext(ctx, query,
		spec.def.Name, spec.scope, nilStringOrNil(spec.teamID), spec.source.Path, spec.def.Enabled, spec.def.AuthType,
		spec.def.SecretEnv, nullableString(spec.def.SignatureHeader), nullableString(spec.def.SignaturePrefix),
		nullableString(spec.def.DeliveryIDHeader), nullableString(spec.def.EventTypeHeader), acceptedEvents,
		spec.def.ActionType, spec.def.ActionPrompt, nullableString(spec.def.ActionAgent),
		nullableInt(spec.def.ActionTimeoutSec), now, id)
	if err != nil {
		return fmt.Errorf("update inbound endpoint %s: %w", spec.source.Path, err)
	}
	return nil
}

// placeholderList returns n dialect placeholders joined by commas.
func (s *Service) placeholderList(n int) string {
	parts := make([]string, n)
	for i := range parts {
		parts[i] = s.inboundPh(i + 1)
	}
	return strings.Join(parts, ", ")
}

// acceptedEventsJSON encodes accepted_events as JSON text, or nil (SQL NULL)
// when the endpoint accepts every event type.
func acceptedEventsJSON(events []string) (any, error) {
	if len(events) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(events)
	if err != nil {
		return nil, fmt.Errorf("marshal accepted events: %w", err)
	}
	return string(raw), nil
}

func nilStringOrNil(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableInt(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}
