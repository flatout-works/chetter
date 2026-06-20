package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetModelCatalogInput struct{}

type ModelCatalogRecord struct {
	DefaultProvider string `json:"default_provider"`
	DefaultModel    string `json:"default_model"`
	ProviderCount   int    `json:"provider_count"`
	ModelCount      int    `json:"model_count"`
	Source          string `json:"source"`
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
	catalog := s.ModelCatalog()
	providers, models := catalog.Counts()
	source := "built-in"
	if s.definitions != nil && s.definitions.Catalog() != nil {
		source = "definitions: " + s.definitions.RepoURL() + " (" + s.definitions.Branch() + ")"
	}
	return nil, GetModelCatalogOutput{
		Catalog: ModelCatalogRecord{
			DefaultProvider: catalog.DefaultProvider,
			DefaultModel:    catalog.DefaultModel,
			ProviderCount:   providers,
			ModelCount:      models,
			Source:          source,
		},
	}, nil
}

func (s *Service) syncDefinitionsTool(ctx context.Context, _ *mcp.CallToolRequest, _ SyncDefinitionsInput) (*mcp.CallToolResult, SyncDefinitionsOutput, error) {
	if !isAdmin(ctx) {
		return nil, SyncDefinitionsOutput{}, fmt.Errorf("admin access required")
	}
	if s.definitions == nil {
		return nil, SyncDefinitionsOutput{}, fmt.Errorf("no definitions repo configured (set DEFINITIONS_REPO)")
	}
	if err := s.definitions.SyncAndLoad(ctx); err != nil {
		slog.Error("definitions sync failed", "err", err)
		return nil, SyncDefinitionsOutput{}, fmt.Errorf("sync definitions: %w", err)
	}
	catalog := s.definitions.Catalog()
	if catalog != nil {
		p, m := catalog.Counts()
		slog.Info("definitions sync complete",
			"default_provider", catalog.DefaultProvider,
			"default_model", catalog.DefaultModel,
			"providers", p,
			"models", m,
		)
	}
	return nil, SyncDefinitionsOutput{
		Message: fmt.Sprintf("definitions synced from %s (%s)", s.definitions.RepoURL(), time.Now().UTC().Format(time.RFC3339)),
	}, nil
}
