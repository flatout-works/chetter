package definitions

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
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
	DefinitionTypeMCPEndpoint  = "mcp_endpoint"
	DefinitionScopeGlobal      = "global"
	DefinitionScopeTeam        = "team"
	DefinitionScopeRepo        = "repo"
)

type Definition struct {
	Type        string
	Name        string
	Scope       string
	TeamName    string
	Repo        string
	Path        string
	Content     string
	ContentHash string
}

type definitionRoot struct {
	path     string
	scope    string
	teamName string
	repo     string
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
	roots, err := m.definitionRoots()
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	for _, root := range roots {
		defs, err := m.scanDefinitionsRoot(root, seen)
		if err != nil {
			return nil, err
		}
		out = append(out, defs...)
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

func (m *Manager) definitionRoots() ([]definitionRoot, error) {
	roots := []definitionRoot{{scope: DefinitionScopeGlobal}}
	if isDir(filepath.Join(m.cacheDir, "global")) {
		roots = append(roots, definitionRoot{path: "global", scope: DefinitionScopeGlobal})
	}
	groupsDir := filepath.Join(m.cacheDir, "groups")
	if isDir(groupsDir) {
		entries, err := os.ReadDir(groupsDir)
		if err != nil {
			return nil, fmt.Errorf("read groups definitions: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() {
				teamName := entry.Name()
				roots = append(roots, definitionRoot{path: filepath.Join("groups", teamName), scope: DefinitionScopeTeam, teamName: teamName})
			}
		}
	}
	reposDir := filepath.Join(m.cacheDir, "repos")
	if isDir(reposDir) {
		owners, err := os.ReadDir(reposDir)
		if err != nil {
			return nil, fmt.Errorf("read repo definitions: %w", err)
		}
		for _, owner := range owners {
			if !owner.IsDir() {
				continue
			}
			ownerDir := filepath.Join(reposDir, owner.Name())
			repos, err := os.ReadDir(ownerDir)
			if err != nil {
				return nil, fmt.Errorf("read repo definitions for %s: %w", owner.Name(), err)
			}
			for _, repo := range repos {
				if repo.IsDir() {
					repoName := owner.Name() + "/" + repo.Name()
					roots = append(roots, definitionRoot{path: filepath.Join("repos", owner.Name(), repo.Name()), scope: DefinitionScopeRepo, repo: repoName})
				}
			}
		}
	}
	return roots, nil
}

func (m *Manager) scanDefinitionsRoot(root definitionRoot, seen map[string]struct{}) ([]Definition, error) {
	var out []Definition
	if root.scope == DefinitionScopeRepo {
		for _, pattern := range []string{filepath.Join("mcp-endpoints", "*.yaml"), filepath.Join("mcp-endpoints", "*.yml")} {
			matches, err := filepath.Glob(filepath.Join(m.cacheDir, root.path, pattern))
			if err != nil {
				return nil, fmt.Errorf("scan scoped MCP endpoints: %w", err)
			}
			if len(matches) > 0 {
				rel, relErr := filepath.Rel(m.cacheDir, matches[0])
				if relErr != nil {
					rel = matches[0]
				}
				return nil, fmt.Errorf("MCP endpoints are global or team scoped; repo-scoped endpoint %s is not supported", filepath.ToSlash(rel))
			}
		}
	}
	type definitionPattern struct {
		definitionType string
		pattern        string
		nameFunc       func(string) string
	}
	patterns := []definitionPattern{
		{DefinitionTypeAgent, filepath.Join("agents", "*.md"), stemName},
		{DefinitionTypeSkill, filepath.Join("skills", "*.md"), stemName},
		{DefinitionTypeTrigger, filepath.Join("triggers", "*.yaml"), stemName},
		{DefinitionTypeTrigger, filepath.Join("triggers", "*.yml"), stemName},
		{DefinitionTypeTaskTemplate, filepath.Join("task-templates", "*.md"), stemName},
	}
	if root.scope == DefinitionScopeGlobal || root.scope == DefinitionScopeTeam {
		patterns = append(patterns,
			definitionPattern{DefinitionTypeMCPEndpoint, filepath.Join("mcp-endpoints", "*.yaml"), stemName},
			definitionPattern{DefinitionTypeMCPEndpoint, filepath.Join("mcp-endpoints", "*.yml"), stemName},
		)
	}
	for _, p := range patterns {
		matches, err := filepath.Glob(filepath.Join(m.cacheDir, root.path, p.pattern))
		if err != nil {
			return nil, fmt.Errorf("scan %s: %w", filepath.Join(root.path, p.pattern), err)
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
			if err := ValidateDefinitionContent(p.definitionType, rel, string(data)); err != nil {
				return nil, err
			}
			sum := sha256.Sum256(data)
			out = append(out, Definition{
				Type:        p.definitionType,
				Name:        p.nameFunc(path),
				Scope:       root.scope,
				TeamName:    root.teamName,
				Repo:        root.repo,
				Path:        rel,
				Content:     string(data),
				ContentHash: hex.EncodeToString(sum[:]),
			})
		}
	}

	// Skills with subdirectories: walk each skill directory to capture all
	// files (SKILL.md plus references/, scripts/, etc.).
	skillDirs, err := filepath.Glob(filepath.Join(m.cacheDir, root.path, "skills", "*"))
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
				Scope:       root.scope,
				TeamName:    root.teamName,
				Repo:        root.repo,
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
	return out, nil
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func stemName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func ValidateDefinitionContent(definitionType, path, content string) error {
	switch definitionType {
	case DefinitionTypeAgent:
		if err := ValidateAgentDefinition(content); err != nil {
			return fmt.Errorf("validate agent definition %s: %w", path, err)
		}
	case DefinitionTypeTrigger:
		if _, err := ParseTriggerYAML(content); err != nil {
			return fmt.Errorf("validate trigger definition %s: %w", path, err)
		}
	case DefinitionTypeMCPEndpoint:
		endpoint, err := ParseMCPEndpointYAML(content)
		if err != nil {
			return fmt.Errorf("validate mcp endpoint definition %s: %w", path, err)
		}
		if endpoint.Name != stemName(path) {
			return fmt.Errorf("validate mcp endpoint definition %s: endpoint name %q must match file name %q", path, endpoint.Name, stemName(path))
		}
	}
	return nil
}

type agentFrontmatter struct {
	Description  any            `yaml:"description"`
	Provider     any            `yaml:"provider"`
	Model        any            `yaml:"model"`
	Mode         any            `yaml:"mode"`
	Identity     any            `yaml:"identity"`
	McpEndpoints any            `yaml:"mcp_endpoints"`
	Permission   map[string]any `yaml:"permission"`
}

func ValidateAgentDefinition(content string) error {
	frontmatter, ok, err := extractYAMLFrontmatter(content)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("agent definitions require YAML frontmatter with an identity")
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(frontmatter), &raw); err != nil {
		return fmt.Errorf("parse frontmatter yaml: %w", err)
	}
	var fm agentFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
		return fmt.Errorf("parse frontmatter fields: %w", err)
	}
	for _, field := range []struct {
		name  string
		value any
	}{
		{"description", fm.Description},
		{"provider", fm.Provider},
		{"model", fm.Model},
		{"mode", fm.Mode},
		{"identity", fm.Identity},
	} {
		if field.value == nil {
			continue
		}
		if _, ok := field.value.(string); !ok {
			return fmt.Errorf("frontmatter field %q must be a string", field.name)
		}
	}
	identity, ok := fm.Identity.(string)
	if !ok || strings.TrimSpace(identity) == "" {
		return fmt.Errorf("frontmatter field %q is required", "identity")
	}
	if _, ok := raw["permission"]; ok {
		if fm.Permission == nil {
			return fmt.Errorf("frontmatter field %q must be an object", "permission")
		}
		for key, value := range fm.Permission {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("frontmatter permission %q must be a string", key)
			}
		}
	}
	if fm.McpEndpoints != nil {
		switch v := fm.McpEndpoints.(type) {
		case []any:
			for _, item := range v {
				if _, ok := item.(string); !ok {
					return fmt.Errorf("frontmatter field %q must be a list of strings", "mcp_endpoints")
				}
			}
		case []string:
		default:
			return fmt.Errorf("frontmatter field %q must be a list of strings", "mcp_endpoints")
		}
	}
	return nil
}

// AgentIdentityName returns the managed Git identity reference declared by an
// agent definition. Validation is kept here so every definition consumer
// applies the same policy.
func AgentIdentityName(content string) (string, error) {
	if err := ValidateAgentDefinition(content); err != nil {
		return "", err
	}
	frontmatter, _, err := extractYAMLFrontmatter(content)
	if err != nil {
		return "", err
	}
	var fm agentFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
		return "", fmt.Errorf("parse frontmatter fields: %w", err)
	}
	return strings.TrimSpace(fm.Identity.(string)), nil
}

// AgentMcpEndpoints returns the MCP endpoint names declared by an agent
// definition's frontmatter. Returns nil if the agent declares no endpoints.
func AgentMcpEndpoints(content string) ([]string, error) {
	if err := ValidateAgentDefinition(content); err != nil {
		return nil, err
	}
	frontmatter, _, err := extractYAMLFrontmatter(content)
	if err != nil {
		return nil, err
	}
	var fm agentFrontmatter
	if err := yaml.Unmarshal([]byte(frontmatter), &fm); err != nil {
		return nil, fmt.Errorf("parse frontmatter fields: %w", err)
	}
	if fm.McpEndpoints == nil {
		return nil, nil
	}
	switch v := fm.McpEndpoints.(type) {
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out, nil
	case []string:
		return v, nil
	default:
		return nil, nil
	}
}

func extractYAMLFrontmatter(content string) (string, bool, error) {
	content = strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(content, "---\n") && !strings.HasPrefix(content, "---\r\n") {
		return "", false, nil
	}
	lines := strings.SplitAfter(content, "\n")
	var start int
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", false, nil
	}
	start = len(lines[0])
	offset := start
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "---" || trimmed == "..." {
			return content[start:offset], true, nil
		}
		offset += len(line)
	}
	return "", false, errors.New("unterminated yaml frontmatter")
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
	Name          string   `yaml:"name"`
	Enabled       *bool    `yaml:"enabled"`
	CronExpr      string   `yaml:"cron_expr"`
	TriggerType   string   `yaml:"trigger_type"`
	TriggerConfig string   `yaml:"trigger_config"`
	Prompt        string   `yaml:"prompt"`
	GitURL        string   `yaml:"git_url"`
	GitRef        string   `yaml:"git_ref"`
	AgentImage    string   `yaml:"agent_image"`
	Agent         string   `yaml:"agent"`
	ProviderID    string   `yaml:"provider_id"`
	ModelID       string   `yaml:"model_id"`
	VariantID     string   `yaml:"variant_id"`
	Harness       string   `yaml:"harness"`
	Skills        any      `yaml:"skills"`
	TimeoutSec    int      `yaml:"timeout_sec"`
	SessionMode   string   `yaml:"session_mode"`
	PauseReason   string   `yaml:"pause_reason"`
	TTLHours      int      `yaml:"ttl_hours"`
	MatchLabels   []string `yaml:"match_labels"`
	Repo          string   `yaml:"repo"`
	Event         string   `yaml:"event"`
}

func ParseTriggerYAML(content string) (TriggerDef, error) {
	var raw rawTriggerYAML
	dec := yaml.NewDecoder(bytes.NewReader([]byte(content)))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return TriggerDef{}, fmt.Errorf("parse trigger yaml: %w", err)
	}

	if raw.Name == "" {
		return TriggerDef{}, fmt.Errorf("trigger name is required")
	}
	enabled := true
	if raw.Enabled != nil {
		enabled = *raw.Enabled
	}

	triggerType := raw.TriggerType
	if triggerType == "" {
		triggerType = "cron"
	}
	switch triggerType {
	case "cron", "pr_review", "issue":
	default:
		return TriggerDef{}, fmt.Errorf("unknown trigger_type %q", triggerType)
	}
	if raw.Harness != "" && !isSupportedHarness(raw.Harness) {
		return TriggerDef{}, fmt.Errorf("unknown harness %q", raw.Harness)
	}
	if raw.TimeoutSec < 0 {
		return TriggerDef{}, fmt.Errorf("timeout_sec must be greater than or equal to 0")
	}
	if raw.TTLHours < 0 {
		return TriggerDef{}, fmt.Errorf("ttl_hours must be greater than or equal to 0")
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
		if raw.SessionMode != "none" && raw.SessionMode != "resumable" {
			return TriggerDef{}, fmt.Errorf("session_mode must be none or resumable")
		}
		runCfg["session_mode"] = raw.SessionMode
	}
	if raw.PauseReason != "" {
		runCfg["pause_reason"] = raw.PauseReason
	}
	if raw.TTLHours > 0 {
		runCfg["ttl_hours"] = raw.TTLHours
	}
	if raw.Repo != "" {
		runCfg["repo"] = raw.Repo
	}
	if raw.Event != "" {
		runCfg["event"] = raw.Event
	}
	if len(raw.MatchLabels) > 0 {
		runCfg["match_labels"] = raw.MatchLabels
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
		Enabled:     enabled,
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

func isSupportedHarness(harness string) bool {
	switch harness {
	case "opencode", "claude-code", "pi", "codewhale", "codex":
		return true
	default:
		return false
	}
}

type MCPEndpointAuth struct {
	Type     string `json:"type"`
	TokenEnv string `json:"token_env"`
}

type MCPEndpointDef struct {
	Name      string            `json:"name"`
	Transport string            `json:"transport"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	Auth      *MCPEndpointAuth  `json:"auth,omitempty"`
}

type rawMCPEndpointAuthYAML struct {
	Type     string `yaml:"type"`
	TokenEnv string `yaml:"token_env"`
}

type rawMCPEndpointYAML struct {
	Name      string                  `yaml:"name"`
	Transport string                  `yaml:"transport"`
	URL       string                  `yaml:"url"`
	Headers   map[string]string       `yaml:"headers"`
	Auth      *rawMCPEndpointAuthYAML `yaml:"auth"`
}

func ParseMCPEndpointYAML(content string) (MCPEndpointDef, error) {
	var raw rawMCPEndpointYAML
	dec := yaml.NewDecoder(bytes.NewReader([]byte(content)))
	dec.KnownFields(true)
	if err := dec.Decode(&raw); err != nil {
		return MCPEndpointDef{}, fmt.Errorf("parse mcp endpoint yaml: %w", err)
	}

	name := strings.TrimSpace(raw.Name)
	if !validMCPEndpointName(name) {
		return MCPEndpointDef{}, fmt.Errorf("mcp endpoint name must start with a letter or number, contain only letters, numbers, dot, underscore, or dash, and be at most 128 characters")
	}
	if reservedMCPEndpointName(name) {
		return MCPEndpointDef{}, fmt.Errorf("mcp endpoint name %q is reserved", name)
	}

	rawURL := strings.TrimSpace(raw.URL)
	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return MCPEndpointDef{}, fmt.Errorf("mcp endpoint %q url must be an absolute http or https URL", name)
	}
	if parsedURL.User != nil {
		return MCPEndpointDef{}, fmt.Errorf("mcp endpoint %q url must not contain credentials", name)
	}
	transport := strings.ToLower(strings.TrimSpace(raw.Transport))
	if transport == "" {
		transport = "http"
	}
	if transport != "http" && transport != "sse" {
		return MCPEndpointDef{}, fmt.Errorf("mcp endpoint %q transport must be http or sse", name)
	}

	headers := make(map[string]string, len(raw.Headers))
	seenHeaders := make(map[string]string, len(raw.Headers))
	for key, value := range raw.Headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return MCPEndpointDef{}, fmt.Errorf("mcp endpoint %q headers must have non-empty names and values", name)
		}
		lookup := strings.ToLower(key)
		if lookup == "authorization" {
			return MCPEndpointDef{}, fmt.Errorf("mcp endpoint %q must configure bearer authorization with auth.token_env", name)
		}
		if previous, ok := seenHeaders[lookup]; ok {
			return MCPEndpointDef{}, fmt.Errorf("mcp endpoint %q has duplicate headers %q and %q", name, previous, key)
		}
		seenHeaders[lookup] = key
		headers[key] = value
	}

	var auth *MCPEndpointAuth
	if raw.Auth != nil {
		authType := strings.ToLower(strings.TrimSpace(raw.Auth.Type))
		if authType != "bearer" {
			return MCPEndpointDef{}, fmt.Errorf("mcp endpoint %q auth.type must be bearer", name)
		}
		tokenEnv := strings.TrimSpace(raw.Auth.TokenEnv)
		if !validEnvName(tokenEnv) {
			return MCPEndpointDef{}, fmt.Errorf("mcp endpoint %q auth.token_env must be a valid environment variable name", name)
		}
		auth = &MCPEndpointAuth{Type: authType, TokenEnv: tokenEnv}
	}

	return MCPEndpointDef{
		Name:      name,
		Transport: transport,
		URL:       rawURL,
		Headers:   headers,
		Auth:      auth,
	}, nil
}

func validMCPEndpointName(name string) bool {
	if name == "" || len(name) > 128 || !asciiAlphaNumeric(name[0]) {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func asciiAlphaNumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' || value >= '0' && value <= '9'
}

func reservedMCPEndpointName(name string) bool {
	return strings.EqualFold(name, "runner-bridge") || strings.EqualFold(name, "chetter")
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}
