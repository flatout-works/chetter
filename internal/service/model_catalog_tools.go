package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/pkg/definitions"
	"github.com/flatout-works/chetter/pkg/modelcatalog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetModelCatalogInput struct{}

type ModelCatalogRecord struct {
	ID              string `json:"id,omitempty"`
	Name            string `json:"name,omitempty"`
	Active          bool   `json:"active,omitempty"`
	DefaultProvider string `json:"default_provider"`
	DefaultModel    string `json:"default_model"`
	ProviderCount   int    `json:"provider_count"`
	ModelCount      int    `json:"model_count"`
	Source          string `json:"source"`
	Checksum        string `json:"checksum,omitempty"`
	TriggerCount    int    `json:"trigger_count"`
}

type GetModelCatalogOutput struct {
	Catalog ModelCatalogRecord `json:"catalog"`
	YAML    string             `json:"yaml,omitempty"`
}

type SyncDefinitionsInput struct {
	Confirm *bool `json:"confirm,omitempty" jsonschema:"Set true to confirm and sync (default true)"`
}

type SyncDefinitionsOutput struct {
	Message string `json:"message"`
}

type ListDefinitionSourcesInput struct{}

type ListDefinitionSourcesOutput struct {
	Sources []DefinitionSourceToolRecord `json:"sources"`
}

type GetDefinitionSourceInput struct {
	SourceID string `json:"source_id,omitempty" jsonschema:"Definition source ID; defaults to the configured default source"`
	Name     string `json:"name,omitempty" jsonschema:"Definition source name; optional alternative to source_id"`
}

type GetDefinitionSourceOutput struct {
	Source DefinitionSourceToolRecord `json:"source"`
}

type SyncDefinitionSourceInput struct {
	SourceID string `json:"source_id,omitempty" jsonschema:"Definition source ID; defaults to the configured default source"`
	Name     string `json:"name,omitempty" jsonschema:"Definition source name; optional alternative to source_id"`
}

type SyncDefinitionSourceOutput struct {
	Source  DefinitionSourceToolRecord `json:"source"`
	Message string                     `json:"message"`
}

type ListDefinitionsInput struct {
	DefinitionType string `json:"definition_type,omitempty" jsonschema:"Optional definition type filter: agent, skill, trigger, task_template, mcp_endpoint"`
	SourceID       string `json:"source_id,omitempty" jsonschema:"Optional definition source ID filter"`
}

type ListDefinitionsOutput struct {
	Definitions []DefinitionToolRecord `json:"definitions"`
}

type GetDefinitionInput struct {
	DefinitionType string `json:"definition_type" jsonschema:"Definition type: agent, skill, trigger, task_template, mcp_endpoint"`
	Name           string `json:"name" jsonschema:"Definition name"`
	SourceID       string `json:"source_id,omitempty" jsonschema:"Definition source ID; defaults to the configured default source"`
	Scope          string `json:"scope,omitempty" jsonschema:"Optional scope filter: global, team, repo. If omitted, returns the highest-priority match."`
}

type GetDefinitionOutput struct {
	Definition DefinitionToolRecord `json:"definition"`
}

type DefinitionSourceToolRecord struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scope      string     `json:"scope"`
	TeamID     string     `json:"team_id,omitempty"`
	Repo       string     `json:"repo,omitempty"`
	RepoURL    string     `json:"repo_url"`
	Branch     string     `json:"branch"`
	Path       string     `json:"path,omitempty"`
	Enabled    bool       `json:"enabled"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	LastSyncAt *time.Time `json:"last_sync_at,omitempty"`
}

type DefinitionToolRecord struct {
	ID             string    `json:"id"`
	SourceID       string    `json:"source_id"`
	DefinitionType string    `json:"definition_type"`
	Name           string    `json:"name"`
	Scope          string    `json:"scope"`
	TeamID         string    `json:"team_id,omitempty"`
	Repo           string    `json:"repo,omitempty"`
	Path           string    `json:"path"`
	SourceCommit   string    `json:"source_commit"`
	SourceRepoURL  string    `json:"source_repo_url,omitempty"`
	SourceBranch   string    `json:"source_branch,omitempty"`
	ContentHash    string    `json:"content_hash"`
	Content        string    `json:"content,omitempty"`
	Active         bool      `json:"active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ListAgentDefinitions returns active agent definitions visible in the current scope.
func (s *Service) ListAgentDefinitions(ctx context.Context, uiTeamIDs, uiRepos []string, name string) ([]DefinitionToolRecord, error) {
	defs, err := s.repo.ListDefinitions(ctx, repository.ListDefinitionsParams{
		Column1:        definitions.DefinitionTypeAgent,
		DefinitionType: definitions.DefinitionTypeAgent,
		Column3:        "",
		SourceID:       "",
		NameFilter:     name,
	})
	if err != nil {
		return nil, fmt.Errorf("list agent definitions: %w", err)
	}

	scope, scoped := auth.GetScope(ctx)
	allowedTeams := uiTeamIDs
	if scoped && !scope.Admin {
		allowedTeams = scope.Teams()
		if len(uiTeamIDs) > 0 {
			allowedTeams = intersectStrings(allowedTeams, uiTeamIDs)
		}
	}
	teamSet := stringSet(allowedTeams)
	repoSet := stringSet(uiRepos)
	sourceCache := map[string]repository.DefinitionSource{}
	out := make([]DefinitionToolRecord, 0, len(defs))
	for _, def := range defs {
		if def.Scope == definitionScopeTeam && len(teamSet) > 0 && (!def.TeamID.Valid || !teamSet[def.TeamID.String]) {
			continue
		}
		if def.Scope == definitionScopeTeam && scoped && !scope.Admin && len(teamSet) == 0 {
			continue
		}
		if def.Scope == definitionScopeRepo && len(repoSet) > 0 && (!def.Repo.Valid || !repoSet[def.Repo.String]) {
			continue
		}
		record := definitionToolRecord(def)
		if def.SourceID != "" {
			source, ok := sourceCache[def.SourceID]
			if !ok {
				source, err = s.repo.GetDefinitionSource(ctx, def.SourceID)
				if err != nil {
					slog.DebugContext(ctx, "get agent definition source", "source_id", def.SourceID, "err", err)
				} else {
					sourceCache[def.SourceID] = source
				}
			}
			if source.ID != "" {
				record.SourceRepoURL = source.RepoUrl
				record.SourceBranch = source.Branch
			}
		}
		out = append(out, record)
	}
	return out, nil
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

const (
	defaultDefinitionSourceID   = "defs_default"
	defaultDefinitionSourceName = "default"
	definitionScopeGlobal       = "global"
	definitionScopeTeam         = "team"
	definitionScopeRepo         = "repo"
	definitionSyncStatusSuccess = "success"
	definitionSyncStatusError   = "error"
)

func (s *Service) getModelCatalogTool(ctx context.Context, _ *mcp.CallToolRequest, _ GetModelCatalogInput) (*mcp.CallToolResult, GetModelCatalogOutput, error) {
	record, yamlText, err := s.activeModelCatalogRecord(ctx)
	if err != nil {
		return nil, GetModelCatalogOutput{}, err
	}
	return nil, GetModelCatalogOutput{Catalog: record, YAML: yamlText}, nil
}

func (s *Service) syncDefinitionsTool(ctx context.Context, _ *mcp.CallToolRequest, _ SyncDefinitionsInput) (*mcp.CallToolResult, SyncDefinitionsOutput, error) {
	if !isAdmin(ctx) {
		return nil, SyncDefinitionsOutput{}, fmt.Errorf("admin access required")
	}
	if s.definitions == nil {
		return nil, SyncDefinitionsOutput{}, fmt.Errorf("no definitions repo configured (set DEFINITIONS_REPO)")
	}
	record, err := s.SyncDefinitions(ctx)
	if err != nil {
		slog.Error("definitions sync failed", "err", err)
		return nil, SyncDefinitionsOutput{}, fmt.Errorf("sync definitions: %w", err)
	}
	return nil, SyncDefinitionsOutput{
		Message: fmt.Sprintf("definitions synced from %s (%s); active catalog %s has %d providers and %d models; %d trigger definitions synced", s.definitions.RepoURL(), time.Now().UTC().Format(time.RFC3339), record.Name, record.ProviderCount, record.ModelCount, record.TriggerCount),
	}, nil
}

func (s *Service) listDefinitionSourcesTool(ctx context.Context, _ *mcp.CallToolRequest, _ ListDefinitionSourcesInput) (*mcp.CallToolResult, ListDefinitionSourcesOutput, error) {
	sources, err := s.repo.ListDefinitionSources(ctx)
	if err != nil {
		return nil, ListDefinitionSourcesOutput{}, fmt.Errorf("list definition sources: %w", err)
	}
	out := make([]DefinitionSourceToolRecord, 0, len(sources))
	for _, source := range sources {
		out = append(out, definitionSourceToolRecord(source))
	}
	return nil, ListDefinitionSourcesOutput{Sources: out}, nil
}

func (s *Service) getDefinitionSourceTool(ctx context.Context, _ *mcp.CallToolRequest, in GetDefinitionSourceInput) (*mcp.CallToolResult, GetDefinitionSourceOutput, error) {
	source, err := s.definitionSourceByInput(ctx, in.SourceID, in.Name)
	if err != nil {
		return nil, GetDefinitionSourceOutput{}, err
	}
	return nil, GetDefinitionSourceOutput{Source: definitionSourceToolRecord(source)}, nil
}

func (s *Service) syncDefinitionSourceTool(ctx context.Context, _ *mcp.CallToolRequest, in SyncDefinitionSourceInput) (*mcp.CallToolResult, SyncDefinitionSourceOutput, error) {
	if !isAdmin(ctx) {
		return nil, SyncDefinitionSourceOutput{}, fmt.Errorf("admin access required")
	}
	if s.definitions == nil {
		return nil, SyncDefinitionSourceOutput{}, fmt.Errorf("no definitions repo configured (set DEFINITIONS_REPO)")
	}
	if in.SourceID != "" && in.SourceID != defaultDefinitionSourceID {
		return nil, SyncDefinitionSourceOutput{}, fmt.Errorf("sync for non-default definition sources is not implemented yet")
	}
	if in.Name != "" && in.Name != defaultDefinitionSourceName {
		return nil, SyncDefinitionSourceOutput{}, fmt.Errorf("sync for non-default definition sources is not implemented yet")
	}
	record, err := s.SyncDefinitions(ctx)
	if err != nil {
		return nil, SyncDefinitionSourceOutput{}, fmt.Errorf("sync definition source: %w", err)
	}
	source, err := s.repo.GetDefinitionSource(ctx, defaultDefinitionSourceID)
	if err != nil {
		return nil, SyncDefinitionSourceOutput{}, fmt.Errorf("get synced definition source: %w", err)
	}
	return nil, SyncDefinitionSourceOutput{
		Source:  definitionSourceToolRecord(source),
		Message: fmt.Sprintf("definition source %s synced; active catalog %s has %d providers and %d models", source.Name, record.Name, record.ProviderCount, record.ModelCount),
	}, nil
}

func (s *Service) listDefinitionsTool(ctx context.Context, _ *mcp.CallToolRequest, in ListDefinitionsInput) (*mcp.CallToolResult, ListDefinitionsOutput, error) {
	defs, err := s.repo.ListDefinitions(ctx, repository.ListDefinitionsParams{
		Column1:        in.DefinitionType,
		DefinitionType: in.DefinitionType,
		Column3:        in.SourceID,
		SourceID:       in.SourceID,
	})
	if err != nil {
		return nil, ListDefinitionsOutput{}, fmt.Errorf("list definitions: %w", err)
	}
	out := make([]DefinitionToolRecord, 0, len(defs))
	for _, def := range defs {
		out = append(out, definitionToolRecord(def))
	}
	return nil, ListDefinitionsOutput{Definitions: out}, nil
}

func (s *Service) getDefinitionTool(ctx context.Context, _ *mcp.CallToolRequest, in GetDefinitionInput) (*mcp.CallToolResult, GetDefinitionOutput, error) {
	if in.DefinitionType == "" {
		return nil, GetDefinitionOutput{}, fmt.Errorf("definition_type is required")
	}
	if in.Name == "" {
		return nil, GetDefinitionOutput{}, fmt.Errorf("name is required")
	}
	sourceID := in.SourceID
	if sourceID == "" {
		sourceID = defaultDefinitionSourceID
	}
	def, err := s.repo.GetDefinitionBySourceTypeName(ctx, repository.GetDefinitionBySourceTypeNameParams{
		SourceID:       sourceID,
		DefinitionType: in.DefinitionType,
		Name:           in.Name,
		ScopeFilter:    in.Scope,
	})
	if err != nil {
		return nil, GetDefinitionOutput{}, fmt.Errorf("get definition: %w", err)
	}
	return nil, GetDefinitionOutput{Definition: definitionToolRecord(def)}, nil
}

func (s *Service) definitionSourceByInput(ctx context.Context, sourceID, name string) (repository.DefinitionSource, error) {
	if name != "" {
		source, err := s.repo.GetDefinitionSourceByName(ctx, name)
		if err != nil {
			return repository.DefinitionSource{}, fmt.Errorf("get definition source by name: %w", err)
		}
		return source, nil
	}
	if sourceID == "" {
		sourceID = defaultDefinitionSourceID
	}
	source, err := s.repo.GetDefinitionSource(ctx, sourceID)
	if err != nil {
		return repository.DefinitionSource{}, fmt.Errorf("get definition source: %w", err)
	}
	return source, nil
}

func (s *Service) SyncDefinitions(ctx context.Context) (ModelCatalogRecord, error) {
	if s.definitions == nil {
		return ModelCatalogRecord{}, fmt.Errorf("no definitions repo configured (set DEFINITIONS_REPO)")
	}
	startedAt := time.Now().UTC()
	if err := s.upsertDefaultDefinitionSource(ctx, startedAt, sql.NullTime{}); err != nil {
		return ModelCatalogRecord{}, err
	}
	if err := s.definitions.Sync(ctx); err != nil {
		s.recordDefinitionSyncRun(ctx, defaultDefinitionSourceID, definitionSyncStatusError, "", 0, err, startedAt, time.Now().UTC())
		return ModelCatalogRecord{}, err
	}
	sourceCommit, err := s.definitions.HeadCommit(ctx)
	if err != nil {
		s.recordDefinitionSyncRun(ctx, defaultDefinitionSourceID, definitionSyncStatusError, "", 0, err, startedAt, time.Now().UTC())
		return ModelCatalogRecord{}, err
	}
	defs, err := s.definitions.ScanDefinitions()
	if err != nil {
		s.recordDefinitionSyncRun(ctx, defaultDefinitionSourceID, definitionSyncStatusError, sourceCommit, 0, err, startedAt, time.Now().UTC())
		return ModelCatalogRecord{}, err
	}
	catalog, yamlText, err := s.definitions.LoadModelCatalogYAML()
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.recordDefinitionSyncRun(ctx, defaultDefinitionSourceID, definitionSyncStatusError, sourceCommit, len(defs), err, startedAt, time.Now().UTC())
		return ModelCatalogRecord{}, err
	}
	now := time.Now().UTC()
	definitionTeamIDs, err := s.definitionTeamIDs(ctx, defs)
	if err != nil {
		s.recordDefinitionSyncRun(ctx, defaultDefinitionSourceID, definitionSyncStatusError, sourceCommit, len(defs), err, startedAt, time.Now().UTC())
		return ModelCatalogRecord{}, err
	}
	triggerEntries, err := s.parseTriggerDefsForSync(defs, now, definitionTeamIDs)
	if err != nil {
		return ModelCatalogRecord{}, fmt.Errorf("parse trigger definitions: %w", err)
	}
	existingTriggers, err := s.repo.ListTriggers(ctx)
	if err != nil {
		return ModelCatalogRecord{}, fmt.Errorf("list existing triggers: %w", err)
	}
	desiredTriggerNames := make(map[string]struct{}, len(triggerEntries))
	for _, entry := range triggerEntries {
		desiredTriggerNames[entry.def.Name] = struct{}{}
	}
	var row repository.InsertModelCatalogParams
	if yamlText != "" {
		checksumBytes := sha256.Sum256([]byte(yamlText))
		row = repository.InsertModelCatalogParams{
			ID:        "mcat_definitions",
			Name:      "definitions",
			Active:    true,
			Source:    nullString(definitionsSource(s.definitions)),
			Checksum:  hex.EncodeToString(checksumBytes[:]),
			Yaml:      yamlText,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}
	if err := withTxRetry(ctx, s.rawDB, s.dialect, func(q data.Repository) error {
		if yamlText != "" {
			if err := q.DeactivateModelCatalogs(ctx, now); err != nil {
				return err
			}
			if err := q.InsertModelCatalog(ctx, row); err != nil {
				return err
			}
		}
		if err := q.DeactivateDefinitionsBySource(ctx, repository.DeactivateDefinitionsBySourceParams{
			UpdatedAt: now,
			SourceID:  defaultDefinitionSourceID,
		}); err != nil {
			return err
		}
		for _, def := range defs {
			scope := definitionScope(def)
			teamID := definitionTeamID(def, definitionTeamIDs)
			if err := q.UpsertDefinition(ctx, repository.UpsertDefinitionParams{
				ID:             definitionID(defaultDefinitionSourceID, def.Type, def.Path),
				SourceID:       defaultDefinitionSourceID,
				DefinitionType: def.Type,
				Name:           def.Name,
				Scope:          scope,
				TeamID:         nullString(teamID),
				Repo:           nullString(def.Repo),
				Path:           def.Path,
				SourceCommit:   sourceCommit,
				ContentHash:    def.ContentHash,
				Content:        def.Content,
				Metadata:       nil,
				Active:         true,
				CreatedAt:      now,
				UpdatedAt:      now,
			}); err != nil {
				return err
			}
		}
		// Remove only triggers that no longer have a definition file. Keeping
		// existing rows preserves trigger IDs and their run history across syncs.
		for _, trigger := range existingTriggers {
			if !trigger.SourceID.Valid || trigger.SourceID.String != defaultDefinitionSourceID {
				continue
			}
			if _, ok := desiredTriggerNames[trigger.Name]; ok {
				continue
			}
			if err := q.DeleteTrigger(ctx, trigger.Name); err != nil {
				return fmt.Errorf("delete orphan trigger %q: %w", trigger.Name, err)
			}
		}
		for _, t := range triggerEntries {
			if err := q.UpsertTrigger(ctx, t.params); err != nil {
				return fmt.Errorf("upsert trigger %q: %w", t.def.Name, err)
			}
		}
		if err := q.MarkDefinitionSourceSynced(ctx, repository.MarkDefinitionSourceSyncedParams{
			LastSyncAt: sql.NullTime{Time: now, Valid: true},
			UpdatedAt:  now,
			ID:         defaultDefinitionSourceID,
		}); err != nil {
			return err
		}
		return q.InsertDefinitionSyncRun(ctx, definitionSyncRunParams(defaultDefinitionSourceID, definitionSyncStatusSuccess, sourceCommit, len(defs), nil, startedAt, now))
	}); err != nil {
		s.recordDefinitionSyncRun(ctx, defaultDefinitionSourceID, definitionSyncStatusError, sourceCommit, len(defs), err, startedAt, time.Now().UTC())
		return ModelCatalogRecord{}, fmt.Errorf("store definitions model catalog: %w", err)
	}
	activateTriggerEntries(ctx, s, triggerEntries)
	s.auditAsync(ctx, AuditEventParams{
		EventType:  "definitions_synced",
		SourceType: "api",
		Detail:     fmt.Sprintf("definitions synced: %d definitions, %d triggers", len(defs), len(triggerEntries)),
	})
	if yamlText == "" {
		record, _, err := s.activeModelCatalogRecord(ctx)
		if err != nil {
			return ModelCatalogRecord{}, err
		}
		return record, nil
	}
	providers, models := catalog.Counts()
	record := ModelCatalogRecord{
		ID:              row.ID,
		Name:            row.Name,
		Active:          row.Active,
		DefaultProvider: catalog.DefaultProvider,
		DefaultModel:    catalog.DefaultModel,
		ProviderCount:   providers,
		ModelCount:      models,
		Source:          row.Source.String,
		Checksum:        row.Checksum,
		TriggerCount:    len(triggerEntries),
	}
	slog.Info("definitions sync complete",
		"default_provider", catalog.DefaultProvider,
		"default_model", catalog.DefaultModel,
		"providers", providers,
		"models", models,
		"definitions", len(defs),
		"triggers", len(triggerEntries),
	)
	return record, nil
}

func (s *Service) upsertDefaultDefinitionSource(ctx context.Context, now time.Time, lastSyncAt sql.NullTime) error {
	return s.repo.UpsertDefinitionSource(ctx, repository.UpsertDefinitionSourceParams{
		ID:         defaultDefinitionSourceID,
		Name:       defaultDefinitionSourceName,
		Scope:      definitionScopeGlobal,
		TeamID:     sql.NullString{},
		Repo:       sql.NullString{},
		RepoUrl:    s.definitions.RepoURL(),
		Branch:     s.definitions.Branch(),
		Path:       "",
		Enabled:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
		LastSyncAt: lastSyncAt,
	})
}

func (s *Service) recordDefinitionSyncRun(ctx context.Context, sourceID, status, sourceCommit string, definitionsCount int, syncErr error, startedAt, endedAt time.Time) {
	if err := s.repo.InsertDefinitionSyncRun(ctx, definitionSyncRunParams(sourceID, status, sourceCommit, definitionsCount, syncErr, startedAt, endedAt)); err != nil {
		slog.Warn("could not record definition sync run", "err", err)
	}
}

func definitionSyncRunParams(sourceID, status, sourceCommit string, definitionsCount int, syncErr error, startedAt, endedAt time.Time) repository.InsertDefinitionSyncRunParams {
	var errString string
	if syncErr != nil {
		errString = syncErr.Error()
	}
	runID, err := randomID("dsync")
	if err != nil {
		runID = "dsync_" + definitionID(sourceID, status, endedAt.Format(time.RFC3339Nano))
	}
	return repository.InsertDefinitionSyncRunParams{
		ID:               runID,
		SourceID:         sourceID,
		Status:           status,
		SourceCommit:     nullString(sourceCommit),
		DefinitionsCount: int32(definitionsCount),
		Error:            nullString(errString),
		StartedAt:        startedAt,
		EndedAt:          endedAt,
		CreatedAt:        endedAt,
	}
}

func definitionID(sourceID, definitionType, path string) string {
	sum := sha256.Sum256([]byte(sourceID + "\x00" + definitionType + "\x00" + path))
	return "def_" + hex.EncodeToString(sum[:])[:32]
}

func (s *Service) definitionTeamIDs(ctx context.Context, defs []definitions.Definition) (map[string]string, error) {
	teamIDs := map[string]string{}
	for _, def := range defs {
		if def.Scope != definitions.DefinitionScopeTeam || def.TeamName == "" {
			continue
		}
		if _, ok := teamIDs[def.TeamName]; ok {
			continue
		}
		team, err := s.repo.GetTeamByName(ctx, def.TeamName)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("definition group %q does not match an existing team", def.TeamName)
			}
			return nil, fmt.Errorf("look up definition group %q: %w", def.TeamName, err)
		}
		teamIDs[def.TeamName] = team.ID
	}
	return teamIDs, nil
}

func definitionScope(def definitions.Definition) string {
	switch def.Scope {
	case definitions.DefinitionScopeTeam:
		return definitionScopeTeam
	case definitions.DefinitionScopeRepo:
		return definitionScopeRepo
	default:
		return definitionScopeGlobal
	}
}

func definitionTeamID(def definitions.Definition, teamIDs map[string]string) string {
	if def.Scope != definitions.DefinitionScopeTeam || def.TeamName == "" {
		return ""
	}
	return teamIDs[def.TeamName]
}

func definitionSourceToolRecord(source repository.DefinitionSource) DefinitionSourceToolRecord {
	return DefinitionSourceToolRecord{
		ID:         source.ID,
		Name:       source.Name,
		Scope:      source.Scope,
		TeamID:     source.TeamID.String,
		Repo:       source.Repo.String,
		RepoURL:    source.RepoUrl,
		Branch:     source.Branch,
		Path:       source.Path,
		Enabled:    source.Enabled,
		CreatedAt:  source.CreatedAt,
		UpdatedAt:  source.UpdatedAt,
		LastSyncAt: nullTimePtr(source.LastSyncAt),
	}
}

func definitionToolRecord(def repository.Definition) DefinitionToolRecord {
	return DefinitionToolRecord{
		ID:             def.ID,
		SourceID:       def.SourceID,
		DefinitionType: def.DefinitionType,
		Name:           def.Name,
		Scope:          def.Scope,
		TeamID:         def.TeamID.String,
		Repo:           def.Repo.String,
		Path:           def.Path,
		SourceCommit:   def.SourceCommit,
		ContentHash:    def.ContentHash,
		Content:        def.Content,
		Active:         def.Active,
		CreatedAt:      def.CreatedAt,
		UpdatedAt:      def.UpdatedAt,
	}
}

func (s *Service) GetModelCatalog(ctx context.Context) (*modelcatalog.Catalog, error) {
	_, catalog, _, err := s.loadActiveModelCatalog(ctx)
	if err != nil {
		return nil, err
	}
	return catalog, nil
}

func (s *Service) activeModelCatalogRecord(ctx context.Context) (ModelCatalogRecord, string, error) {
	row, catalog, yamlText, err := s.loadActiveModelCatalog(ctx)
	if err != nil {
		return ModelCatalogRecord{}, "", err
	}
	providers, models := catalog.Counts()
	return ModelCatalogRecord{
		ID:              row.ID,
		Name:            row.Name,
		Active:          row.Active,
		DefaultProvider: catalog.DefaultProvider,
		DefaultModel:    catalog.DefaultModel,
		ProviderCount:   providers,
		ModelCount:      models,
		Source:          row.Source.String,
		Checksum:        row.Checksum,
	}, yamlText, nil
}

func (s *Service) loadActiveModelCatalog(ctx context.Context) (repository.ChetterModelCatalog, *modelcatalog.Catalog, string, error) {
	row, err := s.repo.GetActiveModelCatalog(ctx)
	if err == nil {
		catalog, parseErr := modelcatalog.ParseYAML([]byte(row.Yaml))
		if parseErr != nil {
			return repository.ChetterModelCatalog{}, nil, "", fmt.Errorf("parse active model catalog: %w", parseErr)
		}
		return row, catalog, row.Yaml, nil
	}
	if err != sql.ErrNoRows {
		return repository.ChetterModelCatalog{}, nil, "", fmt.Errorf("load active model catalog: %w", err)
	}
	return builtInModelCatalogRow()
}

func builtInModelCatalogRow() (repository.ChetterModelCatalog, *modelcatalog.Catalog, string, error) {
	catalog := modelcatalog.Default()
	yamlText, err := modelcatalog.MarshalYAML(catalog)
	if err != nil {
		return repository.ChetterModelCatalog{}, nil, "", err
	}
	checksumBytes := sha256.Sum256([]byte(yamlText))
	now := time.Now().UTC()
	return repository.ChetterModelCatalog{
		ID:        "built-in",
		Name:      "built-in",
		Active:    true,
		Source:    nullString("built-in"),
		Checksum:  hex.EncodeToString(checksumBytes[:]),
		Yaml:      yamlText,
		CreatedAt: now,
		UpdatedAt: now,
	}, catalog, yamlText, nil
}

func definitionsSource(defs interface {
	RepoURL() string
	Branch() string
}) string {
	return "definitions: " + defs.RepoURL() + " (" + defs.Branch() + ")"
}

type triggerSyncEntry struct {
	def    definitions.TriggerDef
	params repository.UpsertTriggerParams
}

func (s *Service) parseTriggerDefsForSync(defs []definitions.Definition, now time.Time, teamIDs map[string]string) ([]triggerSyncEntry, error) {
	var entries []triggerSyncEntry
	for _, def := range defs {
		if def.Type != definitions.DefinitionTypeTrigger {
			continue
		}
		td, err := definitions.ParseTriggerYAML(def.Content)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", def.Path, err)
		}
		id, err := randomID("trig")
		if err != nil {
			return nil, fmt.Errorf("%s: generate trigger id: %w", def.Path, err)
		}
		skillsJSON, err := json.Marshal(nonEmptyStrings(td.Skills))
		if err != nil {
			return nil, fmt.Errorf("%s: marshal skills: %w", def.Path, err)
		}
		teamID := definitionTeamID(def, teamIDs)
		entries = append(entries, triggerSyncEntry{
			def: td,
			params: repository.UpsertTriggerParams{
				ID:            id,
				TeamID:        nullString(teamID),
				Name:          td.Name,
				TriggerType:   td.TriggerType,
				TriggerConfig: json.RawMessage(td.TriggerCfg),
				CronExpr:      td.CronExpr,
				Prompt:        td.Prompt,
				GitUrl:        nullString(td.GitURL),
				GitRef:        nullString(td.GitRef),
				AgentImage:    nullString(s.resolveAgentImage(td.AgentImage)),
				Agent:         nullString(td.Agent),
				ProviderID:    nullString(td.ProviderID),
				ModelID:       nullString(td.ModelID),
				VariantID:     nullString(td.VariantID),
				Harness:       nullString(td.Harness),
				Skills:        skillsJSON,
				TimeoutSec:    int32(td.TimeoutSec),
				Enabled:       td.Enabled,
				SourceID:      nullString(defaultDefinitionSourceID),
				CreatedAt:     now,
				UpdatedAt:     now,
			},
		})
	}
	return entries, nil
}

func activateTriggerEntries(ctx context.Context, s *Service, entries []triggerSyncEntry) {
	for _, t := range entries {
		if t.def.TriggerType != "cron" {
			continue
		}
		trigger, err := s.repo.GetTriggerByName(ctx, t.def.Name)
		if err != nil {
			slog.Warn("activate synced cron trigger: get trigger", "name", t.def.Name, "err", err)
			continue
		}
		if t.def.Enabled {
			record := triggerToStoreRecord(trigger)
			if err := s.activateTrigger(ctx, record); err != nil {
				slog.Warn("activate synced cron trigger", "name", t.def.Name, "err", err)
			}
		} else {
			s.cronMu.Lock()
			if existing, ok := s.cronEntries[trigger.ID]; ok {
				s.cron.Remove(existing)
				delete(s.cronEntries, trigger.ID)
			}
			s.cronMu.Unlock()
		}
	}
}
