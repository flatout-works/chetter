package definitions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/flatout-works/chetter/pkg/modelcatalog"
)

type Manager struct {
	repoURL  string
	branch   string
	cacheDir string
	mu       sync.RWMutex
	catalog  *modelcatalog.Catalog
	rawYAML  string
}

const (
	DefinitionTypeAgent        = "agent"
	DefinitionTypeSkill        = "skill"
	DefinitionTypeTrigger      = "trigger"
	DefinitionTypeTaskTemplate = "task_template"
)

type Definition struct {
	Type        string
	Name        string
	Path        string
	Content     string
	ContentHash string
}

func New(repoURL, branch, cacheDir string) *Manager {
	if branch == "" {
		branch = "main"
	}
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "chetter-definitions")
	}
	return &Manager{
		repoURL:  repoURL,
		branch:   branch,
		cacheDir: cacheDir,
	}
}

func (m *Manager) Sync(ctx context.Context) error {
	info, err := os.Stat(m.cacheDir)
	if err == nil && info.IsDir() {
		cmd := exec.CommandContext(ctx, "git", "pull", "--ff-only", "origin", m.branch)
		cmd.Dir = m.cacheDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git pull: %w\n%s", err, string(out))
		}
		_ = out
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.cacheDir), 0755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", m.branch, m.repoURL, m.cacheDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone: %w\n%s", err, string(out))
	}
	_ = out
	return nil
}

func (m *Manager) HeadCommit(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = m.cacheDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w\n%s", err, string(out))
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *Manager) LoadModelCatalog() (*modelcatalog.Catalog, error) {
	catalog, _, err := m.LoadModelCatalogYAML()
	return catalog, err
}

func (m *Manager) LoadModelCatalogYAML() (*modelcatalog.Catalog, string, error) {
	path := filepath.Join(m.cacheDir, "model-catalog.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read model-catalog.yaml: %w", err)
	}
	catalog, err := modelcatalog.ParseYAML(data)
	if err != nil {
		return nil, "", fmt.Errorf("parse model-catalog.yaml: %w", err)
	}
	return catalog, string(data), nil
}

func (m *Manager) SyncAndLoad(ctx context.Context) error {
	if err := m.Sync(ctx); err != nil {
		return err
	}
	catalog, rawYAML, err := m.LoadModelCatalogYAML()
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.rawYAML = rawYAML
	m.catalog = catalog
	m.mu.Unlock()
	slog.Info("definitions: loaded model catalog",
		"default_provider", catalog.DefaultProvider,
		"default_model", catalog.DefaultModel,
		"providers", len(catalog.Providers),
	)
	return nil
}

func (m *Manager) ScanDefinitions() ([]Definition, error) {
	var out []Definition
	patterns := []struct {
		definitionType string
		pattern        string
		nameFunc       func(string) string
	}{
		{DefinitionTypeAgent, filepath.Join("agents", "*.md"), stemName},
		{DefinitionTypeSkill, filepath.Join("skills", "*.md"), stemName},
		{DefinitionTypeSkill, filepath.Join("skills", "*", "SKILL.md"), parentName},
		{DefinitionTypeTrigger, filepath.Join("triggers", "*.yaml"), stemName},
		{DefinitionTypeTrigger, filepath.Join("triggers", "*.yml"), stemName},
		{DefinitionTypeTaskTemplate, filepath.Join("task-templates", "*.md"), stemName},
	}
	seen := map[string]struct{}{}
	for _, p := range patterns {
		matches, err := filepath.Glob(filepath.Join(m.cacheDir, p.pattern))
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", p.pattern, err)
		}
		for _, path := range matches {
			info, err := os.Stat(path)
			if err != nil {
				return nil, fmt.Errorf("stat definition %s: %w", path, err)
			}
			if info.IsDir() {
				continue
			}
			rel, err := filepath.Rel(m.cacheDir, path)
			if err != nil {
				return nil, fmt.Errorf("relative definition path: %w", err)
			}
			rel = filepath.ToSlash(rel)
			key := p.definitionType + ":" + rel
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read definition %s: %w", rel, err)
			}
			sum := sha256.Sum256(data)
			out = append(out, Definition{
				Type:        p.definitionType,
				Name:        p.nameFunc(path),
				Path:        rel,
				Content:     string(data),
				ContentHash: hex.EncodeToString(sum[:]),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func stemName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func parentName(path string) string {
	return filepath.Base(filepath.Dir(path))
}

func (m *Manager) Catalog() *modelcatalog.Catalog {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.catalog
}

func (m *Manager) CatalogYAML() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rawYAML
}

func (m *Manager) CacheDir() string {
	return m.cacheDir
}

func (m *Manager) RepoURL() string {
	return m.repoURL
}

func (m *Manager) Branch() string {
	return m.branch
}
