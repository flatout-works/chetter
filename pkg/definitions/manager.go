package definitions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/flatout-works/chetter/pkg/modelcatalog"
	"gopkg.in/yaml.v3"
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

func (m *Manager) repoURLWithAuth() string {
	token := os.Getenv("CHETTER_GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GITHUB_TOKEN")
	}
	if token == "" {
		return m.repoURL
	}
	if strings.HasPrefix(m.repoURL, "https://") {
		return "https://x-access-token:" + token + "@" + strings.TrimPrefix(m.repoURL, "https://")
	}
	return m.repoURL
}

func (m *Manager) Sync(ctx context.Context) error {
	url := m.repoURLWithAuth()
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
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--branch", m.branch, url, m.cacheDir)
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

	// Skills with subdirectories: walk each skill directory to capture all
	// files (SKILL.md plus references/, scripts/, etc.).
	skillDirs, err := filepath.Glob(filepath.Join(m.cacheDir, "skills", "*"))
	if err != nil {
		return nil, fmt.Errorf("scan skill dirs: %w", err)
	}
	for _, dir := range skillDirs {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		skillName := filepath.Base(dir)
		err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, err := filepath.Rel(m.cacheDir, path)
			if err != nil {
				return fmt.Errorf("relative path: %w", err)
			}
			rel = filepath.ToSlash(rel)
			key := DefinitionTypeSkill + ":" + rel
			if _, ok := seen[key]; ok {
				return nil
			}
			seen[key] = struct{}{}
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", rel, err)
			}
			sum := sha256.Sum256(data)
			out = append(out, Definition{
				Type:        DefinitionTypeSkill,
				Name:        skillName,
				Path:        rel,
				Content:     string(data),
				ContentHash: hex.EncodeToString(sum[:]),
			})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk skill dir %s: %w", skillName, err)
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

type TriggerDef struct {
	Name        string
	Enabled     bool
	CronExpr    string
	TriggerType string
	TriggerCfg  string // JSON string for trigger_config column
	Prompt      string
	GitURL      string
	GitRef      string
	AgentImage  string
	Agent       string
	ProviderID  string
	ModelID     string
	VariantID   string
	Harness     string
	Skills      []string
	TimeoutSec  int
}

type rawTriggerYAML struct {
	Name          string            `yaml:"name"`
	Enabled       bool              `yaml:"enabled"`
	CronExpr      string            `yaml:"cron_expr"`
	TriggerType   string            `yaml:"trigger_type"`
	TriggerConfig string            `yaml:"trigger_config"`
	Prompt        string            `yaml:"prompt"`
	GitURL        string            `yaml:"git_url"`
	GitRef        string            `yaml:"git_ref"`
	AgentImage    string            `yaml:"agent_image"`
	Agent         string            `yaml:"agent"`
	ProviderID    string            `yaml:"provider_id"`
	ModelID       string            `yaml:"model_id"`
	VariantID     string            `yaml:"variant_id"`
	Harness       string            `yaml:"harness"`
	Skills        any               `yaml:"skills"`
	TimeoutSec    int               `yaml:"timeout_sec"`
	SessionMode   string            `yaml:"session_mode"`
	PauseReason   string            `yaml:"pause_reason"`
	TTLHours      int               `yaml:"ttl_hours"`
	MatchLabels   []string          `yaml:"match_labels"`
	Repo          string            `yaml:"repo"`
	Event         string            `yaml:"event"`
	Extra         map[string]any    `yaml:",inline"`
}

func ParseTriggerYAML(content string) (TriggerDef, error) {
	var raw rawTriggerYAML
	if err := yaml.Unmarshal([]byte(content), &raw); err != nil {
		return TriggerDef{}, fmt.Errorf("parse trigger yaml: %w", err)
	}

	if raw.Name == "" {
		return TriggerDef{}, fmt.Errorf("trigger name is required")
	}
	if !raw.Enabled {
		raw.Enabled = true
	}

	triggerType := raw.TriggerType
	if triggerType == "" {
		triggerType = "cron"
	}

	triggerCfg := raw.TriggerConfig
	if triggerCfg == "" {
		triggerCfg = "{}"
	}

	runCfg := make(map[string]any)
	if raw.TriggerConfig != "" && raw.TriggerConfig != "{}" {
		if err := json.Unmarshal([]byte(raw.TriggerConfig), &runCfg); err != nil {
			return TriggerDef{}, fmt.Errorf("parse trigger_config JSON: %w", err)
		}
	}

	if raw.SessionMode != "" {
		runCfg["session_mode"] = raw.SessionMode
	}
	if raw.PauseReason != "" {
		runCfg["pause_reason"] = raw.PauseReason
	}
	if raw.TTLHours > 0 {
		runCfg["ttl_hours"] = raw.TTLHours
	}

	if len(runCfg) > 0 {
		b, err := json.Marshal(runCfg)
		if err != nil {
			return TriggerDef{}, fmt.Errorf("marshal trigger_config: %w", err)
		}
		triggerCfg = string(b)
	}

	var skills []string
	switch v := raw.Skills.(type) {
	case []any:
		for _, s := range v {
			if str, ok := s.(string); ok {
				skills = append(skills, str)
			}
		}
	case []string:
		skills = v
	case string:
		skills = []string{v}
	}

	return TriggerDef{
		Name:        raw.Name,
		Enabled:     raw.Enabled,
		CronExpr:    raw.CronExpr,
		TriggerType: triggerType,
		TriggerCfg:  triggerCfg,
		Prompt:      raw.Prompt,
		GitURL:      raw.GitURL,
		GitRef:      raw.GitRef,
		AgentImage:  raw.AgentImage,
		Agent:       raw.Agent,
		ProviderID:  raw.ProviderID,
		ModelID:     raw.ModelID,
		VariantID:   raw.VariantID,
		Harness:     raw.Harness,
		Skills:      skills,
		TimeoutSec:  raw.TimeoutSec,
	}, nil
}
