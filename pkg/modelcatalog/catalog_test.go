package modelcatalog

import "testing"

func TestParseYAMLAndRenderOpenCodeProviders(t *testing.T) {
	data := []byte(`version: 1
default_provider: synthetic
default_model: hf:zai-org/GLM-5.2
providers:
  synthetic:
    name: Synthetic
    kind: openai_compatible
    models:
      - id: hf:zai-org/GLM-5.2
  native-only:
    name: Native Only
    kind: native
    models:
      - id: native-model
`)
	catalog, err := ParseYAML(data)
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	providers := catalog.OpenCodeProviders()
	if len(providers) != 1 {
		t.Fatalf("expected 1 opencode provider, got %d", len(providers))
	}
	if providers[0].ID != "synthetic" || providers[0].Models[0] != "hf:zai-org/GLM-5.2" {
		t.Fatalf("unexpected provider render: %+v", providers[0])
	}
}

func TestHarnessDefaults(t *testing.T) {
	catalog := Default()
	provider, model := catalog.DefaultForHarness("pi", "fallback", "fallback-model")
	if provider != "zai" || model != "glm-5.2" {
		t.Fatalf("pi default = %s/%s", provider, model)
	}
	provider, model = catalog.DefaultForHarness("unknown", "fallback", "fallback-model")
	if provider != "synthetic" || model != "hf:zai-org/GLM-5.2" {
		t.Fatalf("global default = %s/%s", provider, model)
	}
	provider, model = catalog.DefaultForHarness("codewhale", "fallback", "fallback-model")
	if provider != "deepseek" || model != "deepseek-chat" {
		t.Fatalf("codewhale default = %s/%s", provider, model)
	}
}

func TestParseYAMLRejectsUnknownFields(t *testing.T) {
	data := []byte(`version: 1
default_provider: synthetic
default_model: hf:zai-org/GLM-5.2
providers:
  synthetic:
    name: Synthetic
    kind: openai_compatible
    surprise: true
    models:
      - id: hf:zai-org/GLM-5.2
`)
	if _, err := ParseYAML(data); err == nil {
		t.Fatal("expected unknown model catalog field to fail")
	}
}

func TestParseYAMLRejectsUnsupportedHarnessAPI(t *testing.T) {
	data := []byte(`version: 1
default_provider: synthetic
default_model: hf:zai-org/GLM-5.2
providers:
  synthetic:
    models:
      - id: hf:zai-org/GLM-5.2
    harnesses:
      pi:
        api: unsupported
`)
	if _, err := ParseYAML(data); err == nil {
		t.Fatal("expected unsupported harness api to fail")
	}
}

func TestParseYAMLLiteLLMProviderAllHarnesses(t *testing.T) {
	data := []byte(`version: 1
default_provider: litellm
default_model: coding-model
defaults:
  opencode:
    provider: litellm
    model: coding-model
  pi:
    provider: litellm
    model: coding-model
  claude-code:
    provider: litellm
    model: coding-model
providers:
  litellm:
    name: Corporate LiteLLM
    kind: openai_compatible
    api_key_env: LITELLM_API_KEY
    models:
      - id: coding-model
    harnesses:
      opencode:
        id: litellm
        api: openai-completions
        base_url: https://litellm.example.com/v1
      pi:
        id: litellm
        api: openai-completions
        auth_header: true
        base_url: https://litellm.example.com/v1
      claude-code:
        id: litellm
        api: anthropic-messages
        base_url: https://litellm.example.com
      codewhale:
        disabled: true
`)
	catalog, err := ParseYAML(data)
	if err != nil {
		t.Fatalf("ParseYAML: %v", err)
	}
	p := catalog.Providers["litellm"]
	if p.Harnesses["opencode"].API != "openai-completions" {
		t.Fatalf("opencode api = %q", p.Harnesses["opencode"].API)
	}
	if !p.Harnesses["pi"].AuthHeader {
		t.Fatal("pi auth_header should be true")
	}
	if p.Harnesses["claude-code"].API != "anthropic-messages" {
		t.Fatalf("claude-code api = %q", p.Harnesses["claude-code"].API)
	}
	if !p.Harnesses["codewhale"].Disabled {
		t.Fatal("codewhale should be disabled")
	}
}
