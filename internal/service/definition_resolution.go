package service

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
)

type scopedDefinitionGroup struct {
	score     int
	updatedAt time.Time
	rows      []repository.Definition
}

func selectScopedDefinitionGroups(ctx context.Context, db *sql.DB, defType string, names []string, teamID, gitURL string) (map[string][]repository.Definition, error) {
	names = uniqueNonEmptyStrings(names)
	if len(names) == 0 {
		return map[string][]repository.Definition{}, nil
	}
	placeholders := strings.Repeat(",?", len(names))[1:]
	query := `SELECT id, source_id, definition_type, name, scope, team_id, repo, path, source_commit, content_hash, content, metadata, active, created_at, updated_at
		FROM definitions
		WHERE definition_type=? AND name IN (` + placeholders + `) AND active=true`
	args := make([]any, 0, len(names)+1)
	args = append(args, defType)
	for _, name := range names {
		args = append(args, name)
	}
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	repoKeys := repoIdentitySet(gitURL)
	grouped := make(map[string]map[string]*scopedDefinitionGroup, len(names))
	for rows.Next() {
		var def repository.Definition
		if err := rows.Scan(
			&def.ID,
			&def.SourceID,
			&def.DefinitionType,
			&def.Name,
			&def.Scope,
			&def.TeamID,
			&def.Repo,
			&def.Path,
			&def.SourceCommit,
			&def.ContentHash,
			&def.Content,
			&def.Metadata,
			&def.Active,
			&def.CreatedAt,
			&def.UpdatedAt,
		); err != nil {
			return nil, err
		}
		score := definitionScopeScore(def, teamID, repoKeys)
		if score == 0 {
			continue
		}
		sourceGroups := grouped[def.Name]
		if sourceGroups == nil {
			sourceGroups = map[string]*scopedDefinitionGroup{}
			grouped[def.Name] = sourceGroups
		}
		key := fmt.Sprintf("%d\x00%s", score, def.SourceID)
		group := sourceGroups[key]
		if group == nil {
			group = &scopedDefinitionGroup{score: score}
			sourceGroups[key] = group
		}
		if def.UpdatedAt.After(group.updatedAt) {
			group.updatedAt = def.UpdatedAt
		}
		group.rows = append(group.rows, def)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	selected := make(map[string][]repository.Definition, len(grouped))
	for name, sourceGroups := range grouped {
		var best *scopedDefinitionGroup
		for _, group := range sourceGroups {
			if best == nil ||
				group.score > best.score ||
				(group.score == best.score && group.updatedAt.After(best.updatedAt)) {
				best = group
			}
		}
		if best != nil {
			selected[name] = best.rows
		}
	}
	return selected, nil
}

func definitionScopeScore(def repository.Definition, teamID string, repoKeys map[string]struct{}) int {
	switch strings.TrimSpace(def.Scope) {
	case "", definitionScopeGlobal:
		return 1
	case "team":
		if teamID != "" && def.TeamID.Valid && def.TeamID.String == teamID {
			return 2
		}
	case "repo":
		if definitionTeamApplies(def, teamID) && def.Repo.Valid && repoMatches(def.Repo.String, repoKeys) {
			return 3
		}
	}
	return 0
}

func definitionTeamApplies(def repository.Definition, teamID string) bool {
	if !def.TeamID.Valid || strings.TrimSpace(def.TeamID.String) == "" {
		return true
	}
	return teamID != "" && def.TeamID.String == teamID
}

func definitionLookupRef(gitURL string, env map[string]string) string {
	if env != nil {
		if repo := strings.TrimSpace(env[definitionRepoEnv]); repo != "" {
			return repo
		}
	}
	return gitURL
}

func repoMatches(repo string, taskRepos map[string]struct{}) bool {
	if len(taskRepos) == 0 {
		return false
	}
	for key := range repoIdentitySet(repo) {
		if _, ok := taskRepos[key]; ok {
			return true
		}
	}
	return false
}

func canonicalRepoName(raw string) (string, bool) {
	ids := repoIdentitySet(raw)
	if len(ids) == 0 {
		return "", false
	}
	value := strings.TrimSpace(raw)
	for _, key := range []string{strings.ToLower(value), strings.ToLower(strings.TrimSuffix(value, ".git"))} {
		if _, ok := ids[key]; ok && strings.Count(key, "/") == 1 {
			return key, true
		}
	}
	for key := range ids {
		if strings.Count(key, "/") == 1 {
			return key, true
		}
	}
	return "", false
}

func repoIdentitySet(raw string) map[string]struct{} {
	out := map[string]struct{}{}
	value := strings.TrimSpace(raw)
	if value == "" {
		return out
	}
	value = strings.TrimSuffix(value, ".git")
	if strings.HasPrefix(value, "git@") {
		if after, ok := strings.CutPrefix(value, "git@"); ok {
			if host, path, found := strings.Cut(after, ":"); found {
				addRepoIdentities(out, host, path)
				return out
			}
		}
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		addRepoIdentities(out, parsed.Host, parsed.Path)
		return out
	}
	parts := strings.Split(strings.Trim(value, "/"), "/")
	if len(parts) >= 3 && strings.Contains(parts[0], ".") {
		addRepoIdentities(out, parts[0], strings.Join(parts[1:], "/"))
		return out
	}
	if len(parts) >= 2 {
		ownerRepo := strings.ToLower(parts[len(parts)-2] + "/" + parts[len(parts)-1])
		ownerRepo = strings.TrimSuffix(ownerRepo, ".git")
		out[ownerRepo] = struct{}{}
	}
	return out
}

func addRepoIdentities(out map[string]struct{}, host, path string) {
	host = strings.ToLower(strings.TrimSpace(host))
	path = strings.Trim(strings.TrimSpace(path), "/")
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return
	}
	ownerRepo := strings.ToLower(parts[len(parts)-2] + "/" + parts[len(parts)-1])
	out[ownerRepo] = struct{}{}
	if host != "" {
		out[host+"/"+ownerRepo] = struct{}{}
	}
}
