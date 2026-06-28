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
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
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
	DefinitionType string `json:"definition_type,omitempty" jsonschema:"Optional definition type filter: agent, skill, trigger, task_template, mcp_profile"`
	SourceID       string `json:"source_id,omitempty" jsonschema:"Optional definition source ID filter"`
}

type ListDefinitionsOutput struct {
	Definitions []DefinitionToolRecord `json:"definitions"`
}

type GetDefinitionInput struct {
	DefinitionType string `json:"definition_type" jsonschema:"Definition type: agent, skill, trigger, task_template, mcp_profile"`
	Name           string `json:"name" jsonschema:"Definition name"`
	SourceID       string `json:"source_id,omitempty" jsonschema:"Definition source ID; defaults to the configured default source"`
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
	ContentHash    string    `json:"content_hash"`
	Content        string    `json:"content,omitempty"`
	Active         bool      `json:"active"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

const (
	defaultDefinitionSourceID   = "defs_default"
	defaultDefinitionSourceName = "default"
	definitionScopeGlobal       = "global"
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
	if in.DefinitionType == definitions.DefinitionTypeMCPProfile && !isAdmin(ctx) {
		return nil, ListDefinitionsOutput{}, fmt.Errorf("admin access required")
	}
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
		if def.DefinitionType == definitions.DefinitionTypeMCPProfile && !isAdmin(ctx) {
			continue
		}
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
	if in.DefinitionType == definitions.DefinitionTypeMCPProfile && !isAdmin(ctx) {
		return nil, GetDefinitionOutput{}, fmt.Errorf("admin access required")
	}
	sourceID := in.SourceID
	if sourceID == "" {
		sourceID = defaultDefinitionSourceID
	}
	def, err := s.repo.GetDefinitionBySourceTypeName(ctx, repository.GetDefinitionBySourceTypeNameParams{
		SourceID:       sourceID,
		DefinitionType: in.DefinitionType,
		Name:           in.Name,
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
	mcpProfileNames, err := validateMCPProfileDefsForSync(defs)
	if err != nil {
		s.recordDefinitionSyncRun(ctx, defaultDefinitionSourceID, definitionSyncStatusError, sourceCommit, len(defs), err, startedAt, time.Now().UTC())
		return ModelCatalogRecord{}, err
	}
	triggerEntries, err := parseTriggerDefsForSync(defs, now)
	if err != nil {
		err = fmt.Errorf("parse trigger definitions: %w", err)
		s.recordDefinitionSyncRun(ctx, defaultDefinitionSourceID, definitionSyncStatusError, sourceCommit, len(defs), err, startedAt, time.Now().UTC())
		return ModelCatalogRecord{}, err
	}
	if err := validateTriggerMCPProfileRefs(triggerEntries, mcpProfileNames); err != nil {
		s.recordDefinitionSyncRun(ctx, defaultDefinitionSourceID, definitionSyncStatusError, sourceCommit, len(defs), err, startedAt, time.Now().UTC())
		return ModelCatalogRecord{}, err
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
	var staleSyncedTriggerIDs []string
	if err := withTxRetryDB(ctx, s.rawDB, func(q *repository.Queries, tx *sql.Tx) error {
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
			if err := q.UpsertDefinition(ctx, repository.UpsertDefinitionParams{
				ID:             definitionID(defaultDefinitionSourceID, def.Type, def.Path),
				SourceID:       defaultDefinitionSourceID,
				DefinitionType: def.Type,
				Name:           def.Name,
				Scope:          definitionScopeGlobal,
				TeamID:         sql.NullString{},
				Repo:           sql.NullString{},
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
		if err := validateTriggerSyncOwnership(ctx, q, triggerEntries); err != nil {
			return err
		}
		for _, t := range triggerEntries {
			if err := q.UpsertTrigger(ctx, t.params); err != nil {
				return fmt.Errorf("upsert trigger %q: %w", t.def.Name, err)
			}
		}
		ids, err := disableStaleSyncedTriggers(ctx, tx, triggerEntries, now)
		if err != nil {
			return err
		}
		staleSyncedTriggerIDs = ids
		return nil
	}); err != nil {
		s.recordDefinitionSyncRun(ctx, defaultDefinitionSourceID, definitionSyncStatusError, sourceCommit, len(defs), err, startedAt, time.Now().UTC())
		return ModelCatalogRecord{}, fmt.Errorf("store definitions model catalog: %w", err)
	}
	s.removeCronEntries(staleSyncedTriggerIDs)
	if err := activateTriggerEntries(ctx, s, triggerEntries); err != nil {
		s.recordDefinitionSyncRun(ctx, defaultDefinitionSourceID, definitionSyncStatusError, sourceCommit, len(defs), err, startedAt, time.Now().UTC())
		return ModelCatalogRecord{}, err
	}
	completedAt := time.Now().UTC()
	if err := withTxRetryDB(ctx, s.rawDB, func(q *repository.Queries, _ *sql.Tx) error {
		if err := q.MarkDefinitionSourceSynced(ctx, repository.MarkDefinitionSourceSyncedParams{
			LastSyncAt: sql.NullTime{Time: completedAt, Valid: true},
			UpdatedAt:  completedAt,
			ID:         defaultDefinitionSourceID,
		}); err != nil {
			return err
		}
		return q.InsertDefinitionSyncRun(ctx, definitionSyncRunParams(defaultDefinitionSourceID, definitionSyncStatusSuccess, sourceCommit, len(defs), nil, startedAt, completedAt))
	}); err != nil {
		s.recordDefinitionSyncRun(ctx, defaultDefinitionSourceID, definitionSyncStatusError, sourceCommit, len(defs), err, startedAt, time.Now().UTC())
		return ModelCatalogRecord{}, fmt.Errorf("record definitions sync success: %w", err)
	}
	s.auditAsync(AuditEventParams{
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

func validateTriggerSyncOwnership(ctx context.Context, q *repository.Queries, entries []triggerSyncEntry) error {
	for _, entry := range entries {
		existing, err := q.GetTriggerByName(ctx, entry.def.Name)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			return fmt.Errorf("check existing trigger %q: %w", entry.def.Name, err)
		}
		if existing.SourceID.Valid && existing.SourceID.String == defaultDefinitionSourceID {
			continue
		}
		source := "dynamic"
		if existing.SourceID.Valid && existing.SourceID.String != "" {
			source = existing.SourceID.String
		}
		return fmt.Errorf("trigger %q already exists from %s source; rename it or remove the conflicting Git trigger", entry.def.Name, source)
	}
	return nil
}

func disableStaleSyncedTriggers(ctx context.Context, tx *sql.Tx, entries []triggerSyncEntry, now time.Time) ([]string, error) {
	names := make([]string, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		name := strings.TrimSpace(entry.def.Name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	where, args := staleSyncedTriggerWhere(names)
	rows, err := tx.QueryContext(ctx, `SELECT id FROM chetter_triggers WHERE `+where, args...)
	if err != nil {
		return nil, fmt.Errorf("select stale synced triggers: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan stale synced trigger: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close stale synced trigger rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan stale synced triggers: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	updateArgs := append([]any{now}, args...)
	if _, err := tx.ExecContext(ctx, `UPDATE chetter_triggers SET enabled=false, updated_at=? WHERE `+where, updateArgs...); err != nil {
		return nil, fmt.Errorf("disable stale synced triggers: %w", err)
	}
	return ids, nil
}

func staleSyncedTriggerWhere(names []string) (string, []any) {
	args := []any{defaultDefinitionSourceID}
	where := `source_id=?`
	if len(names) > 0 {
		placeholders := strings.Repeat(",?", len(names))[1:]
		where += ` AND name NOT IN (` + placeholders + `)`
		for _, name := range names {
			args = append(args, name)
		}
	}
	return where, args
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

func validateMCPProfileDefsForSync(defs []definitions.Definition) (map[string]struct{}, error) {
	profiles := make(map[string]struct{})
	var problems []string
	for _, def := range defs {
		if def.Type != definitions.DefinitionTypeMCPProfile {
			continue
		}
		profile, err := definitions.ParseMCPProfileYAML(def.Content)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", def.Path, err))
			continue
		}
		if profile.Name != def.Name {
			problems = append(problems, fmt.Sprintf("%s: profile name %q does not match definition name %q", def.Path, profile.Name, def.Name))
			continue
		}
		if _, exists := profiles[profile.Name]; exists {
			problems = append(problems, fmt.Sprintf("%s: duplicate mcp profile name %q", def.Path, profile.Name))
			continue
		}
		profiles[profile.Name] = struct{}{}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid mcp profile definitions: %s", strings.Join(problems, "; "))
	}
	return profiles, nil
}

func validateTriggerMCPProfileRefs(entries []triggerSyncEntry, profiles map[string]struct{}) error {
	var problems []string
	for _, entry := range entries {
		for _, profileName := range nonEmptyStrings(entry.def.MCPProfiles) {
			if _, ok := profiles[profileName]; !ok {
				problems = append(problems, fmt.Sprintf("trigger %q references missing mcp profile %q", entry.def.Name, profileName))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid trigger mcp profile references: %s", strings.Join(problems, "; "))
	}
	return nil
}

func parseTriggerDefsForSync(defs []definitions.Definition, now time.Time) ([]triggerSyncEntry, error) {
	var entries []triggerSyncEntry
	var problems []string
	seenNames := make(map[string]string)
	for _, def := range defs {
		if def.Type != definitions.DefinitionTypeTrigger {
			continue
		}
		td, err := definitions.ParseTriggerYAML(def.Content)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", def.Path, err))
			continue
		}
		if err := validateTriggerDefForSync(def.Path, td); err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if previousPath, ok := seenNames[td.Name]; ok {
			problems = append(problems, fmt.Sprintf("%s: duplicate trigger name %q (already defined in %s)", def.Path, td.Name, previousPath))
			continue
		}
		seenNames[td.Name] = def.Path
		id, err := randomID("trig")
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: generate id: %v", def.Path, err))
			continue
		}
		skillsJSON, err := json.Marshal(nonEmptyStrings(td.Skills))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: marshal skills: %v", def.Path, err))
			continue
		}
		mcpProfilesJSON, err := json.Marshal(nonEmptyStrings(td.MCPProfiles))
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: marshal mcp_profiles: %v", def.Path, err))
			continue
		}
		entries = append(entries, triggerSyncEntry{
			def: td,
			params: repository.UpsertTriggerParams{
				ID:            id,
				TeamID:        sql.NullString{},
				Name:          td.Name,
				TriggerType:   td.TriggerType,
				TriggerConfig: json.RawMessage(td.TriggerCfg),
				CronExpr:      td.CronExpr,
				Prompt:        td.Prompt,
				GitUrl:        nullString(td.GitURL),
				GitRef:        nullString(td.GitRef),
				AgentImage:    nullString(td.AgentImage),
				Agent:         nullString(td.Agent),
				ProviderID:    nullString(td.ProviderID),
				ModelID:       nullString(td.ModelID),
				VariantID:     nullString(td.VariantID),
				Harness:       nullString(td.Harness),
				Skills:        skillsJSON,
				McpProfiles:   mcpProfilesJSON,
				TimeoutSec:    int32(td.TimeoutSec),
				Enabled:       td.Enabled,
				SourceID:      nullString(defaultDefinitionSourceID),
				CreatedAt:     now,
				UpdatedAt:     now,
			},
		})
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid trigger definitions: %s", strings.Join(problems, "; "))
	}
	return entries, nil
}

func validateTriggerDefForSync(path string, td definitions.TriggerDef) error {
	switch td.TriggerType {
	case store.TriggerTypeCron:
		if strings.TrimSpace(td.Prompt) == "" {
			return fmt.Errorf("%s: prompt is required for cron triggers", path)
		}
		if strings.TrimSpace(td.CronExpr) == "" {
			return fmt.Errorf("%s: cron_expr is required for cron triggers", path)
		}
		if _, err := defaultCronParser.Parse(td.CronExpr); err != nil {
			return fmt.Errorf("%s: parse cron: %w", path, err)
		}
	case store.TriggerTypePRReview:
		var cfg store.PRReviewTriggerConfig
		if err := json.Unmarshal([]byte(td.TriggerCfg), &cfg); err != nil || strings.TrimSpace(cfg.Repo) == "" {
			return fmt.Errorf("%s: repo is required in trigger_config for pr_review triggers", path)
		}
	case store.TriggerTypeIssue:
		var cfg struct {
			Repo string `json:"repo"`
		}
		if err := json.Unmarshal([]byte(td.TriggerCfg), &cfg); err != nil || strings.TrimSpace(cfg.Repo) == "" {
			return fmt.Errorf("%s: repo is required in trigger_config for issue triggers", path)
		}
	default:
		return fmt.Errorf("%s: unknown trigger_type %q", path, td.TriggerType)
	}
	return nil
}

func activateTriggerEntries(ctx context.Context, s *Service, entries []triggerSyncEntry) error {
	var problems []string
	for _, t := range entries {
		if t.def.TriggerType != store.TriggerTypeCron {
			continue
		}
		trigger, err := s.repo.GetTriggerByName(ctx, t.def.Name)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: get trigger: %v", t.def.Name, err))
			continue
		}
		if t.def.Enabled {
			record := triggerToStoreRecord(trigger)
			if err := s.activateTrigger(ctx, record); err != nil {
				problems = append(problems, fmt.Sprintf("%s: %v", t.def.Name, err))
			}
		} else {
			s.removeCronEntries([]string{trigger.ID})
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("activate synced cron triggers: %s", strings.Join(problems, "; "))
	}
	return nil
}
