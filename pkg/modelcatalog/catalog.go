package modelcatalog

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const EnvKey = "CHETTER_MODEL_CATALOG_YAML"

type Catalog struct {
	Version         int                       `yaml:"version" json:"version"`
	DefaultProvider string                    `yaml:"default_provider" json:"default_provider"`
	DefaultModel    string                    `yaml:"default_model" json:"default_model"`
	Defaults        map[string]HarnessDefault `yaml:"defaults" json:"defaults"`
	Providers       map[string]Provider       `yaml:"providers" json:"providers"`
}

type HarnessDefault struct {
	Provider string `yaml:"provider" json:"provider"`
	Model    string `yaml:"model" json:"model"`
}

type Provider struct {
	Name      string                     `yaml:"name" json:"name"`
	Kind      string                     `yaml:"kind" json:"kind"`
	BaseURL   string                     `yaml:"base_url" json:"base_url"`
	APIKeyEnv string                     `yaml:"api_key_env" json:"api_key_env"`
	Models    []Model                    `yaml:"models" json:"models"`
	Harnesses map[string]ProviderHarness `yaml:"harnesses" json:"harnesses"`
}

type ProviderHarness struct {
	ID        string `yaml:"id" json:"id"`
	Name      string `yaml:"name" json:"name"`
	BaseURL   string `yaml:"base_url" json:"base_url"`
	APIKeyEnv string `yaml:"api_key_env" json:"api_key_env"`
	Disabled  bool   `yaml:"disabled" json:"disabled"`
}

type Model struct {
	ID        string                  `yaml:"id" json:"id"`
	Name      string                  `yaml:"name" json:"name"`
	Aliases   []string                `yaml:"aliases" json:"aliases"`
	Harnesses map[string]ModelHarness `yaml:"harnesses" json:"harnesses"`
}

type ModelHarness struct {
	ID       string         `yaml:"id" json:"id"`
	Disabled bool           `yaml:"disabled" json:"disabled"`
	Options  map[string]any `yaml:"options" json:"options"`
}

type OpenCodeProvider struct {
	ID        string
	Name      string
	BaseURL   string
	APIKeyEnv string
	Models    []string
}

func ParseYAML(data []byte) (*Catalog, error) {
	var c Catalog
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse model catalog yaml: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func ParseYAMLOrDefault(data string) *Catalog {
	if strings.TrimSpace(data) != "" {
		catalog, err := ParseYAML([]byte(data))
		if err == nil {
			return catalog
		}
	}
	return Default()
}

func MarshalYAML(catalog *Catalog) (string, error) {
	if catalog == nil {
		catalog = Default()
	}
	data, err := yaml.Marshal(catalog)
	if err != nil {
		return "", fmt.Errorf("marshal model catalog yaml: %w", err)
	}
	return string(data), nil
}

func Default() *Catalog {
	return &Catalog{
		Version:         1,
		DefaultProvider: "synthetic",
		DefaultModel:    "hf:zai-org/GLM-5.2",
		Defaults: map[string]HarnessDefault{
			"opencode":    {Provider: "synthetic", Model: "hf:zai-org/GLM-5.2"},
			"pi":          {Provider: "zai", Model: "glm-5.2"},
			"claude-code": {Provider: "anthropic", Model: "claude-sonnet-4-5"},
			"codewhale":   {Provider: "deepseek", Model: "deepseek-chat"},
		},
		Providers: map[string]Provider{
			"synthetic": {
				Name:   "Synthetic",
				Kind:   "openai_compatible",
				Models: []Model{{ID: "hf:zai-org/GLM-5.2"}},
			},
			"opencode": {
				Name:      "OpenCode Zen",
				Kind:      "openai_compatible",
				BaseURL:   "https://opencode.ai/zen/v1",
				APIKeyEnv: "OPENCODE_API_KEY",
				Models:    []Model{{ID: "deepseek-v4-flash-free"}},
			},
			"deepseek": {
				Name:      "DeepSeek",
				Kind:      "openai_compatible",
				BaseURL:   "https://api.deepseek.com",
				APIKeyEnv: "DEEPSEEK_API_KEY",
				Models: []Model{
					{ID: "deepseek-chat"},
					{ID: "deepseek-v4-pro"},
					{ID: "deepseek-v4-flash"},
				},
			},
			"zai": {
				Name:   "Z.ai",
				Kind:   "native",
				Models: []Model{{ID: "glm-5.2"}},
			},
			"anthropic": {
				Name:      "Anthropic",
				Kind:      "native",
				APIKeyEnv: "ANTHROPIC_API_KEY",
				Models:    []Model{{ID: "claude-sonnet-4-5"}},
			},
		},
	}
}

func (c Catalog) Validate() error {
	if c.Version < 1 {
		return fmt.Errorf("model catalog version is required")
	}
	if strings.TrimSpace(c.DefaultProvider) == "" {
		return fmt.Errorf("default_provider is required")
	}
	if strings.TrimSpace(c.DefaultModel) == "" {
		return fmt.Errorf("default_model is required")
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}
	provider, ok := c.Providers[c.DefaultProvider]
	if !ok {
		return fmt.Errorf("default_provider %q is not defined", c.DefaultProvider)
	}
	foundDefaultModel := false
	for id, p := range c.Providers {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("provider id must not be empty")
		}
		if len(p.Models) == 0 {
			return fmt.Errorf("provider %q must define at least one model", id)
		}
		seenModels := map[string]struct{}{}
		for _, m := range p.Models {
			if strings.TrimSpace(m.ID) == "" {
				return fmt.Errorf("provider %q has a model with empty id", id)
			}
			if _, exists := seenModels[m.ID]; exists {
				return fmt.Errorf("provider %q has duplicate model %q", id, m.ID)
			}
			seenModels[m.ID] = struct{}{}
		}
	}
	for _, m := range provider.Models {
		if m.ID == c.DefaultModel {
			foundDefaultModel = true
			break
		}
	}
	if !foundDefaultModel {
		return fmt.Errorf("default_model %q is not defined for provider %q", c.DefaultModel, c.DefaultProvider)
	}
	for harness, def := range c.Defaults {
		if strings.TrimSpace(def.Provider) == "" || strings.TrimSpace(def.Model) == "" {
			return fmt.Errorf("default for harness %q must include provider and model", harness)
		}
		provider, ok := c.Providers[def.Provider]
		if !ok {
			return fmt.Errorf("default provider %q for harness %q is not defined", def.Provider, harness)
		}
		found := false
		for _, m := range provider.Models {
			if m.ID == def.Model {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("default model %q for harness %q is not defined for provider %q", def.Model, harness, def.Provider)
		}
	}
	return nil
}

func (c Catalog) Counts() (providers, models int) {
	providers = len(c.Providers)
	for _, p := range c.Providers {
		models += len(p.Models)
	}
	return providers, models
}

func (c Catalog) DefaultForHarness(harness, fallbackProvider, fallbackModel string) (string, string) {
	providerID := c.DefaultProvider
	modelID := c.DefaultModel
	if def, ok := c.Defaults[harness]; ok {
		providerID = def.Provider
		modelID = def.Model
	}
	if p, ok := c.Providers[providerID]; ok {
		if hp, ok := p.Harnesses[harness]; ok && !hp.Disabled && hp.ID != "" {
			providerID = hp.ID
		}
		for _, m := range p.Models {
			if m.ID != modelID {
				continue
			}
			if hm, ok := m.Harnesses[harness]; ok && !hm.Disabled && hm.ID != "" {
				modelID = hm.ID
			}
			break
		}
	}
	if providerID == "" {
		providerID = fallbackProvider
	}
	if modelID == "" {
		modelID = fallbackModel
	}
	return providerID, modelID
}

func (c Catalog) OpenCodeProviders() []OpenCodeProvider {
	ids := make([]string, 0, len(c.Providers))
	for id := range c.Providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]OpenCodeProvider, 0, len(ids))
	for _, id := range ids {
		p := c.Providers[id]
		hp := p.Harnesses["opencode"]
		if p.Kind != "" && p.Kind != "openai_compatible" && hp.ID == "" {
			continue
		}
		if hp.Disabled {
			continue
		}
		providerID := firstNonEmpty(hp.ID, id)
		provider := OpenCodeProvider{
			ID:        providerID,
			Name:      firstNonEmpty(hp.Name, p.Name, providerID),
			BaseURL:   firstNonEmpty(hp.BaseURL, p.BaseURL),
			APIKeyEnv: firstNonEmpty(hp.APIKeyEnv, p.APIKeyEnv),
		}
		seenModels := map[string]struct{}{}
		for _, m := range p.Models {
			hm := m.Harnesses["opencode"]
			if hm.Disabled {
				continue
			}
			modelID := firstNonEmpty(hm.ID, m.ID)
			if modelID == "" {
				continue
			}
			if _, ok := seenModels[modelID]; ok {
				continue
			}
			seenModels[modelID] = struct{}{}
			provider.Models = append(provider.Models, modelID)
		}
		if len(provider.Models) > 0 {
			out = append(out, provider)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
