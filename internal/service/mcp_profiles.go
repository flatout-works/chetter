package service

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/flatout-works/chetter/pkg/definitions"
)

const maxTaskMCPProfiles = 16

func normalizeMCPProfileNames(values []string) []string {
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

func loadGlobalMCPProfiles(ctx context.Context, db *sql.DB, requested []string) ([]definitions.MCPProfileDef, error) {
	names := normalizeMCPProfileNames(requested)
	if len(names) == 0 {
		return nil, nil
	}
	if len(names) > maxTaskMCPProfiles {
		return nil, fmt.Errorf("at most %d MCP profiles may be attached to a task", maxTaskMCPProfiles)
	}

	placeholders := strings.Repeat(",?", len(names))[1:]
	query := `SELECT name, content FROM definitions
		WHERE definition_type = 'mcp_profile'
		  AND scope = 'global'
		  AND team_id IS NULL
		  AND repo IS NULL
		  AND active = true
		  AND name IN (` + placeholders + `)`
	args := make([]any, len(names))
	for i, name := range names {
		args[i] = name
	}

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query global MCP profiles: %w", err)
	}
	defer rows.Close()

	byName := make(map[string]definitions.MCPProfileDef, len(names))
	for rows.Next() {
		var name, content string
		if err := rows.Scan(&name, &content); err != nil {
			return nil, fmt.Errorf("scan global MCP profile: %w", err)
		}
		if _, exists := byName[name]; exists {
			return nil, fmt.Errorf("multiple active global MCP profiles named %q", name)
		}
		profile, err := definitions.ParseMCPProfileYAML(content)
		if err != nil {
			return nil, fmt.Errorf("parse global MCP profile %q: %w", name, err)
		}
		if profile.Name != name {
			return nil, fmt.Errorf("global MCP profile %q contains mismatched name %q", name, profile.Name)
		}
		byName[name] = profile
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read global MCP profiles: %w", err)
	}

	missing := make([]string, 0)
	out := make([]definitions.MCPProfileDef, 0, len(names))
	for _, name := range names {
		profile, ok := byName[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		out = append(out, profile)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return nil, fmt.Errorf("active global MCP profiles not found: %s", strings.Join(missing, ", "))
	}
	return out, nil
}
