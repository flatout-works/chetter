package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
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
		Message: fmt.Sprintf("definitions synced from %s (%s); active catalog %s has %d providers and %d models", s.definitions.RepoURL(), time.Now().UTC().Format(time.RFC3339), record.Name, record.ProviderCount, record.ModelCount),
	}, nil
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
	if err := withTxRetry(ctx, s.rawDB, func(q *repository.Queries) error {
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
	}
	slog.Info("definitions sync complete",
		"default_provider", catalog.DefaultProvider,
		"default_model", catalog.DefaultModel,
		"providers", providers,
		"models", models,
		"definitions", len(defs),
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
