package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"log/slog"
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
	if err := s.definitions.SyncAndLoad(ctx); err != nil {
		return ModelCatalogRecord{}, err
	}
	yamlText := s.definitions.CatalogYAML()
	if yamlText == "" {
		return ModelCatalogRecord{}, fmt.Errorf("definitions repo did not provide model-catalog.yaml")
	}
	catalog, err := modelcatalog.ParseYAML([]byte(yamlText))
	if err != nil {
		return ModelCatalogRecord{}, err
	}
	checksumBytes := sha256.Sum256([]byte(yamlText))
	now := time.Now().UTC()
	row := repository.InsertModelCatalogParams{
		ID:        "mcat_definitions",
		Name:      "definitions",
		Active:    true,
		Source:    nullString(definitionsSource(s.definitions)),
		Checksum:  hex.EncodeToString(checksumBytes[:]),
		Yaml:      yamlText,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := withTxRetry(ctx, s.rawDB, func(q *repository.Queries) error {
		if err := q.DeactivateModelCatalogs(ctx, now); err != nil {
			return err
		}
		return q.InsertModelCatalog(ctx, row)
	}); err != nil {
		return ModelCatalogRecord{}, fmt.Errorf("store definitions model catalog: %w", err)
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
	)
	return record, nil
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
