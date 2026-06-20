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
}
