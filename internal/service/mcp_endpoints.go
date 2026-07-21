package service

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/pkg/definitions"
)

const maxTaskMcpEndpoints = 16

func normalizeMcpEndpointNames(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

// loadMcpEndpoints resolves requested endpoint names from the definitions
// table, scoped to global and (when teamID is set) the task's team scope.
func loadMcpEndpoints(ctx context.Context, db *sql.DB, dialect store.Dialect, requested []string, teamID string) ([]definitions.MCPEndpointDef, error) {
	names := normalizeMcpEndpointNames(requested)
	if len(names) == 0 {
		return nil, nil
	}
	if len(names) > maxTaskMcpEndpoints {
		return nil, fmt.Errorf("at most %d MCP endpoints may be attached to a task", maxTaskMcpEndpoints)
	}

	namePlaceholders := strings.Repeat(",?", len(names))[1:]
	query := `SELECT name, scope, content FROM definitions
		WHERE definition_type = 'mcp_endpoint'
		  AND active = true
		  AND name IN (` + namePlaceholders + `)`
	args := make([]any, len(names))
	for i, name := range names {
		args[i] = name
	}
	if teamID != "" {
		query += ` AND ((scope = 'global' AND team_id IS NULL) OR (scope = 'team' AND team_id = ?))`
		args = append(args, teamID)
	} else {
		query += ` AND (scope = 'global' AND team_id IS NULL)`
	}
	query = sqlQuery(dialect, query)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query mcp endpoints: %w", err)
	}
	defer rows.Close()

	type resolvedEndpoint struct {
		scope    string
		endpoint definitions.MCPEndpointDef
	}
	byName := make(map[string]resolvedEndpoint, len(names))
	for rows.Next() {
		var name, scope, content string
		if err := rows.Scan(&name, &scope, &content); err != nil {
			return nil, fmt.Errorf("scan mcp endpoint: %w", err)
		}
		endpoint, err := definitions.ParseMCPEndpointYAML(content)
		if err != nil {
			return nil, fmt.Errorf("parse mcp endpoint %q: %w", name, err)
		}
		if endpoint.Name != name {
			return nil, fmt.Errorf("mcp endpoint %q contains mismatched name %q", name, endpoint.Name)
		}
		if previous, exists := byName[name]; exists {
			if previous.scope == "team" && scope == "global" {
				continue
			}
			if previous.scope == "global" && scope == "team" {
				byName[name] = resolvedEndpoint{scope: scope, endpoint: endpoint}
				continue
			}
			return nil, fmt.Errorf("multiple active MCP endpoints named %q in scope %s", name, scope)
		}
		byName[name] = resolvedEndpoint{scope: scope, endpoint: endpoint}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read mcp endpoints: %w", err)
	}

	missing := make([]string, 0)
	out := make([]definitions.MCPEndpointDef, 0, len(names))
	for _, name := range names {
		resolved, ok := byName[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		out = append(out, resolved.endpoint)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("active MCP endpoints not found: %s", strings.Join(missing, ", "))
	}
	return out, nil
}
