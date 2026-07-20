package service

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/pkg/definitions"
)

var gitIdentityNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,127}$`)

// GitIdentityInput describes a non-secret Git author identity. GitHub App
// credentials are resolved by the server and are never stored in this record.
type GitIdentityInput struct {
	TeamID         string
	TeamName       string
	Name           string
	GitAuthorName  string
	GitAuthorEmail string
	CredentialType string
}

type GitIdentityRecord struct {
	ID             string    `json:"id"`
	TeamID         string    `json:"team_id,omitempty"`
	Name           string    `json:"name"`
	GitAuthorName  string    `json:"git_author_name"`
	GitAuthorEmail string    `json:"git_author_email"`
	CredentialType string    `json:"credential_type"`
	IsDefault      bool      `json:"is_default"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (s *Service) CreateGitIdentity(ctx context.Context, in GitIdentityInput) (GitIdentityRecord, error) {
	if err := validateGitIdentityInput(in); err != nil {
		return GitIdentityRecord{}, err
	}
	teamID, err := s.resolveOwnerTeamID(ctx, in.TeamID, in.TeamName)
	if err != nil {
		return GitIdentityRecord{}, err
	}
	id, err := randomID("gid")
	if err != nil {
		return GitIdentityRecord{}, fmt.Errorf("generate Git identity id: %w", err)
	}
	now := time.Now().UTC()
	_, err = s.rawDB.ExecContext(ctx, sqlQuery(s.dialect, `INSERT INTO git_identities
         (id, team_id, name, git_author_name, git_author_email, credential_type, created_at, updated_at)
	         VALUES (?, ?, ?, ?, ?, ?, ?, ?)`), id, teamID, in.Name, in.GitAuthorName, in.GitAuthorEmail, normalizedCredentialType(in.CredentialType), now, now)
	if err != nil {
		return GitIdentityRecord{}, fmt.Errorf("insert Git identity: %w", err)
	}
	record, err := s.gitIdentityByName(ctx, teamID, in.Name, false)
	if err != nil {
		return GitIdentityRecord{}, err
	}
	s.auditAsync(ctx, AuditEventParams{EventType: "git_identity_created", SourceType: "api", TargetType: "git_identity", TargetID: record.ID, Detail: record.Name})
	return record, nil
}

func (s *Service) ListGitIdentities(ctx context.Context) ([]GitIdentityRecord, error) {
	scope, scoped := auth.GetScope(ctx)
	query := `SELECT id, team_id, name, git_author_name, git_author_email, credential_type, is_default, created_at, updated_at FROM git_identities`
	args := []any{}
	if scoped && !scope.Admin {
		teams := scope.Teams()
		if len(teams) == 0 {
			return nil, nil
		}
		placeholders := strings.TrimRight(strings.Repeat("?,", len(teams)), ",")
		query += " WHERE team_id='' OR team_id IN (" + placeholders + ")"
		for _, teamID := range teams {
			args = append(args, teamID)
		}
	}
	query += " ORDER BY team_id, name"
	query = sqlQuery(s.dialect, query)
	rows, err := s.rawDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list Git identities: %w", err)
	}
	defer rows.Close()
	var records []GitIdentityRecord
	for rows.Next() {
		var record GitIdentityRecord
		if err := rows.Scan(&record.ID, &record.TeamID, &record.Name, &record.GitAuthorName, &record.GitAuthorEmail, &record.CredentialType, &record.IsDefault, &record.CreatedAt, &record.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan Git identity: %w", err)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Service) SetDefaultGitIdentity(ctx context.Context, teamID, teamName, name string) (GitIdentityRecord, error) {
	resolvedTeamID, err := s.resolveOwnerTeamID(ctx, teamID, teamName)
	if err != nil {
		return GitIdentityRecord{}, err
	}
	record, err := s.gitIdentityByName(ctx, resolvedTeamID, name, false)
	if err != nil {
		return GitIdentityRecord{}, err
	}
	tx, err := s.rawDB.BeginTx(ctx, nil)
	if err != nil {
		return GitIdentityRecord{}, fmt.Errorf("begin Git identity default transaction: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, sqlQuery(s.dialect, `UPDATE git_identities SET is_default=false, updated_at=? WHERE team_id=?`), time.Now().UTC(), resolvedTeamID); err != nil {
		return GitIdentityRecord{}, fmt.Errorf("clear Git identity default: %w", err)
	}
	if _, err := tx.ExecContext(ctx, sqlQuery(s.dialect, `UPDATE git_identities SET is_default=true, updated_at=? WHERE id=?`), time.Now().UTC(), record.ID); err != nil {
		return GitIdentityRecord{}, fmt.Errorf("set Git identity default: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return GitIdentityRecord{}, fmt.Errorf("commit Git identity default: %w", err)
	}
	record.IsDefault = true
	record.UpdatedAt = time.Now().UTC()
	s.auditAsync(ctx, AuditEventParams{EventType: "git_identity_default_set", SourceType: "api", TargetType: "git_identity", TargetID: record.ID, Detail: record.Name})
	return record, nil
}

func (s *Service) GetGitIdentity(ctx context.Context, teamID, teamName, name string) (GitIdentityRecord, error) {
	resolvedTeamID, err := s.resolveOwnerTeamID(ctx, teamID, teamName)
	if err != nil {
		return GitIdentityRecord{}, err
	}
	return s.gitIdentityByName(ctx, resolvedTeamID, name, false)
}

func (s *Service) UpdateGitIdentity(ctx context.Context, in GitIdentityInput) (GitIdentityRecord, error) {
	if err := validateGitIdentityInput(in); err != nil {
		return GitIdentityRecord{}, err
	}
	teamID, err := s.resolveOwnerTeamID(ctx, in.TeamID, in.TeamName)
	if err != nil {
		return GitIdentityRecord{}, err
	}
	result, err := s.rawDB.ExecContext(ctx, sqlQuery(s.dialect, `UPDATE git_identities SET git_author_name=?, git_author_email=?, credential_type=?, updated_at=? WHERE team_id=? AND name=?`), in.GitAuthorName, in.GitAuthorEmail, normalizedCredentialType(in.CredentialType), time.Now().UTC(), teamID, in.Name)
	if err != nil {
		return GitIdentityRecord{}, fmt.Errorf("update Git identity: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return GitIdentityRecord{}, fmt.Errorf("git identity %q not found", in.Name)
	}
	record, err := s.gitIdentityByName(ctx, teamID, in.Name, false)
	if err != nil {
		return GitIdentityRecord{}, err
	}
	s.auditAsync(ctx, AuditEventParams{EventType: "git_identity_updated", SourceType: "api", TargetType: "git_identity", TargetID: record.ID, Detail: record.Name})
	return record, nil
}

func (s *Service) DeleteGitIdentity(ctx context.Context, teamID, teamName, name string) error {
	teamID, err := s.resolveOwnerTeamID(ctx, teamID, teamName)
	if err != nil {
		return err
	}
	record, err := s.gitIdentityByName(ctx, teamID, name, false)
	if err != nil {
		return err
	}
	var references int
	if err := s.rawDB.QueryRowContext(ctx, sqlQuery(s.dialect, `SELECT COUNT(*) FROM chetter_tasks WHERE git_identity_id=?`), record.ID).Scan(&references); err != nil {
		return fmt.Errorf("check Git identity usage: %w", err)
	}
	if references > 0 {
		return fmt.Errorf("git identity %q is used by %d task(s)", name, references)
	}
	result, err := s.rawDB.ExecContext(ctx, sqlQuery(s.dialect, `DELETE FROM git_identities WHERE id=?`), record.ID)
	if err != nil {
		return fmt.Errorf("delete Git identity: %w", err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("git identity %q not found", name)
	}
	s.auditAsync(ctx, AuditEventParams{EventType: "git_identity_deleted", SourceType: "api", TargetType: "git_identity", TargetID: record.ID, Detail: record.Name})
	return nil
}

func (s *Service) resolveTaskGitIdentity(ctx context.Context, agent, teamID, gitURL string) (GitIdentityRecord, error) {
	if strings.TrimSpace(agent) == "" {
		return GitIdentityRecord{}, fmt.Errorf("agent is required")
	}
	repo := repoNameFromGitURL(gitURL)
	var content string
	err := s.rawDB.QueryRowContext(ctx, sqlQuery(s.dialect, `SELECT content FROM definitions
         WHERE definition_type='agent' AND name=? AND active=true
         AND (scope='global' OR (scope='team' AND team_id=?) OR (scope='repo' AND repo=?))
         ORDER BY CASE WHEN scope='repo' AND repo=? THEN 3 WHEN scope='team' AND team_id=? THEN 2 ELSE 1 END DESC, updated_at DESC
	         LIMIT 1`), agent, teamID, repo, repo, teamID).Scan(&content)
	if err != nil {
		if err == sql.ErrNoRows {
			return GitIdentityRecord{}, fmt.Errorf("active agent definition %q not found for this team or repository", agent)
		}
		return GitIdentityRecord{}, fmt.Errorf("resolve agent definition: %w", err)
	}
	identityName, err := definitions.AgentIdentityName(content)
	if err != nil {
		return GitIdentityRecord{}, fmt.Errorf("agent definition %q: %w", agent, err)
	}
	return s.gitIdentityByName(ctx, teamID, identityName, true)
}

func (s *Service) defaultGitIdentity(ctx context.Context, teamID string) (GitIdentityRecord, error) {
	query := `SELECT id, team_id, name, git_author_name, git_author_email, credential_type, is_default, created_at, updated_at FROM git_identities WHERE is_default=true AND team_id=?`
	args := []any{teamID}
	if teamID != "" {
		query = `SELECT id, team_id, name, git_author_name, git_author_email, credential_type, is_default, created_at, updated_at FROM git_identities WHERE is_default=true AND team_id IN (?, '') ORDER BY CASE WHEN team_id=? THEN 0 ELSE 1 END LIMIT 1`
		args = []any{teamID, teamID}
	}
	query = sqlQuery(s.dialect, query)
	var record GitIdentityRecord
	err := s.rawDB.QueryRowContext(ctx, query, args...).Scan(&record.ID, &record.TeamID, &record.Name, &record.GitAuthorName, &record.GitAuthorEmail, &record.CredentialType, &record.IsDefault, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return GitIdentityRecord{}, fmt.Errorf("no default Git identity is configured")
		}
		return GitIdentityRecord{}, fmt.Errorf("get default Git identity: %w", err)
	}
	return record, nil
}

func (s *Service) gitIdentityByName(ctx context.Context, teamID, name string, allowGlobal bool) (GitIdentityRecord, error) {
	if strings.TrimSpace(name) == "" {
		return GitIdentityRecord{}, fmt.Errorf("git identity name is required")
	}
	query := `SELECT id, team_id, name, git_author_name, git_author_email, credential_type, is_default, created_at, updated_at FROM git_identities WHERE name=? AND team_id=?`
	args := []any{name, teamID}
	if allowGlobal && teamID != "" {
		query = `SELECT id, team_id, name, git_author_name, git_author_email, credential_type, is_default, created_at, updated_at FROM git_identities WHERE name=? AND team_id IN (?, '') ORDER BY CASE WHEN team_id=? THEN 0 ELSE 1 END LIMIT 1`
		args = []any{name, teamID, teamID}
	}
	query = sqlQuery(s.dialect, query)
	var record GitIdentityRecord
	err := s.rawDB.QueryRowContext(ctx, query, args...).Scan(&record.ID, &record.TeamID, &record.Name, &record.GitAuthorName, &record.GitAuthorEmail, &record.CredentialType, &record.IsDefault, &record.CreatedAt, &record.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return GitIdentityRecord{}, fmt.Errorf("git identity %q not found", name)
		}
		return GitIdentityRecord{}, fmt.Errorf("get Git identity: %w", err)
	}
	return record, nil
}

func validateGitIdentityInput(in GitIdentityInput) error {
	if !gitIdentityNamePattern.MatchString(in.Name) {
		return fmt.Errorf("identity name must use lowercase letters, digits, and hyphens")
	}
	if strings.TrimSpace(in.GitAuthorName) == "" || strings.TrimSpace(in.GitAuthorEmail) == "" {
		return fmt.Errorf("git_author_name and git_author_email are required")
	}
	if !strings.Contains(in.GitAuthorEmail, "@") {
		return fmt.Errorf("git_author_email must be an email address")
	}
	if normalizedCredentialType(in.CredentialType) != "github_app" {
		return fmt.Errorf("credential_type must be github_app")
	}
	return nil
}

func normalizedCredentialType(value string) string {
	if strings.TrimSpace(value) == "" {
		return "github_app"
	}
	return strings.TrimSpace(value)
}

func repoNameFromGitURL(gitURL string) string {
	gitURL = strings.TrimSuffix(strings.TrimSpace(gitURL), "/")
	gitURL = strings.TrimSuffix(gitURL, ".git")
	if i := strings.Index(gitURL, "github.com/"); i >= 0 {
		return gitURL[i+len("github.com/"):]
	}
	if i := strings.Index(gitURL, ":"); i >= 0 {
		return gitURL[i+1:]
	}
	return ""
}
