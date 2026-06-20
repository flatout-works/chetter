package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/pkg/modelcatalog"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ImportModelCatalogInput struct {
	Name     string `json:"name,omitempty" jsonschema:"Catalog name (default: default)"`
	YAML     string `json:"yaml,omitempty" jsonschema:"Model catalog YAML content"`
	FilePath string `json:"file_path,omitempty" jsonschema:"Server-local YAML file path to import"`
	Activate *bool  `json:"activate,omitempty" jsonschema:"Activate this catalog after import (default true)"`
}

type GetModelCatalogInput struct {
	Name string `json:"name,omitempty" jsonschema:"Catalog name. Omit to get the active catalog."`
}

type ModelCatalogRecord struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Active          bool      `json:"active"`
	Source          string    `json:"source,omitempty"`
	Checksum        string    `json:"checksum"`
	DefaultProvider string    `json:"default_provider"`
	DefaultModel    string    `json:"default_model"`
	ProviderCount   int       `json:"provider_count"`
	ModelCount      int       `json:"model_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type ImportModelCatalogOutput struct {
	Catalog ModelCatalogRecord `json:"catalog"`
}

type GetModelCatalogOutput struct {
	Catalog ModelCatalogRecord `json:"catalog"`
	YAML    string             `json:"yaml"`
}

type ListModelCatalogsInput struct{}

type ListModelCatalogsOutput struct {
	Catalogs []ModelCatalogRecord `json:"catalogs"`
}

func (s *Service) importModelCatalogTool(ctx context.Context, _ *mcp.CallToolRequest, in ImportModelCatalogInput) (*mcp.CallToolResult, ImportModelCatalogOutput, error) {
	if !isAdmin(ctx) {
		return nil, ImportModelCatalogOutput{}, fmt.Errorf("admin access required")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "default"
	}
	yamlText, source, err := modelCatalogYAMLInput(in)
	if err != nil {
		return nil, ImportModelCatalogOutput{}, err
	}
	catalog, err := modelcatalog.ParseYAML([]byte(yamlText))
	if err != nil {
		return nil, ImportModelCatalogOutput{}, err
	}
	activate := true
	if in.Activate != nil {
		activate = *in.Activate
	}
	now := time.Now().UTC()
	checksumBytes := sha256.Sum256([]byte(yamlText))
	checksum := hex.EncodeToString(checksumBytes[:])
	var id string
	err = withTxRetry(ctx, s.rawDB, func(q *repository.Queries) error {
		if activate {
			if err := q.DeactivateModelCatalogs(ctx, now); err != nil {
				return err
			}
		}
		existing, err := q.GetModelCatalogByName(ctx, name)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if err == nil {
			id = existing.ID
		} else {
			id, err = randomID("mcat")
			if err != nil {
				return err
			}
		}
		return q.InsertModelCatalog(ctx, repository.InsertModelCatalogParams{
			ID:        id,
			Name:      name,
			Active:    activate,
			Source:    nullString(source),
			Checksum:  checksum,
			Yaml:      yamlText,
			CreatedAt: now,
			UpdatedAt: now,
		})
	})
	if err != nil {
		return nil, ImportModelCatalogOutput{}, fmt.Errorf("import model catalog: %w", err)
	}
	return nil, ImportModelCatalogOutput{Catalog: modelCatalogRecord(id, name, activate, source, checksum, *catalog, now, now)}, nil
}

func (s *Service) getModelCatalogTool(ctx context.Context, _ *mcp.CallToolRequest, in GetModelCatalogInput) (*mcp.CallToolResult, GetModelCatalogOutput, error) {
	if !isAdmin(ctx) {
		return nil, GetModelCatalogOutput{}, fmt.Errorf("admin access required")
	}
	var row repository.ChetterModelCatalog
	var err error
	if strings.TrimSpace(in.Name) == "" {
		row, err = s.repo.GetActiveModelCatalog(ctx)
	} else {
		row, err = s.repo.GetModelCatalogByName(ctx, strings.TrimSpace(in.Name))
	}
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, GetModelCatalogOutput{}, fmt.Errorf("model catalog not found")
		}
		return nil, GetModelCatalogOutput{}, fmt.Errorf("get model catalog: %w", err)
	}
	catalog, err := modelcatalog.ParseYAML([]byte(row.Yaml))
	if err != nil {
		return nil, GetModelCatalogOutput{}, err
	}
	return nil, GetModelCatalogOutput{Catalog: modelCatalogRow(row, *catalog), YAML: row.Yaml}, nil
}

func (s *Service) listModelCatalogsTool(ctx context.Context, _ *mcp.CallToolRequest, _ ListModelCatalogsInput) (*mcp.CallToolResult, ListModelCatalogsOutput, error) {
	if !isAdmin(ctx) {
		return nil, ListModelCatalogsOutput{}, fmt.Errorf("admin access required")
	}
	rows, err := s.repo.ListModelCatalogs(ctx)
	if err != nil {
		return nil, ListModelCatalogsOutput{}, fmt.Errorf("list model catalogs: %w", err)
	}
	out := make([]ModelCatalogRecord, 0, len(rows))
	for _, row := range rows {
		catalog, err := modelcatalog.ParseYAML([]byte(row.Yaml))
		if err != nil {
			continue
		}
		out = append(out, modelCatalogRecord(row.ID, row.Name, row.Active, row.Source.String, row.Checksum, *catalog, row.CreatedAt, row.UpdatedAt))
	}
	return nil, ListModelCatalogsOutput{Catalogs: out}, nil
}

func modelCatalogYAMLInput(in ImportModelCatalogInput) (yamlText, source string, err error) {
	if strings.TrimSpace(in.YAML) != "" && strings.TrimSpace(in.FilePath) != "" {
		return "", "", fmt.Errorf("provide either yaml or file_path, not both")
	}
	if strings.TrimSpace(in.YAML) != "" {
		return in.YAML, "inline", nil
	}
	if strings.TrimSpace(in.FilePath) != "" {
		data, err := os.ReadFile(in.FilePath)
		if err != nil {
			return "", "", fmt.Errorf("read model catalog file: %w", err)
		}
		return string(data), in.FilePath, nil
	}
	return "", "", fmt.Errorf("yaml or file_path is required")
}

func modelCatalogRow(row repository.ChetterModelCatalog, catalog modelcatalog.Catalog) ModelCatalogRecord {
	return modelCatalogRecord(row.ID, row.Name, row.Active, row.Source.String, row.Checksum, catalog, row.CreatedAt, row.UpdatedAt)
}

func modelCatalogRecord(id, name string, active bool, source, checksum string, catalog modelcatalog.Catalog, createdAt, updatedAt time.Time) ModelCatalogRecord {
	providers, models := catalog.Counts()
	return ModelCatalogRecord{
		ID:              id,
		Name:            name,
		Active:          active,
		Source:          source,
		Checksum:        checksum,
		DefaultProvider: catalog.DefaultProvider,
		DefaultModel:    catalog.DefaultModel,
		ProviderCount:   providers,
		ModelCount:      models,
		CreatedAt:       createdAt,
		UpdatedAt:       updatedAt,
	}
}
