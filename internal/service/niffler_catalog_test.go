package service

import (
	"testing"

	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/pkg/modelcatalog"
)

func TestCatalogHarnessNameChoices(t *testing.T) {
	for _, name := range []string{"opencode", "claude-code", "pi", "codewhale", "codex", "niffler", "unknown"} {
		if got := catalogHarnessName(" " + name + " "); got != name {
			t.Fatalf("catalogHarnessName(%q) = %q", name, got)
		}
	}
	if got := catalogHarnessName(" "); got != "opencode" {
		t.Fatalf("default harness = %q", got)
	}
}

func TestResolveModelForTaskNiffler(t *testing.T) {
	catalog := modelcatalog.Default()
	got := resolveModelForTask(catalog, &runnerv1.Task{Harness: "niffler"})
	if got.ProviderID != "deepseek" || got.ModelID != "deepseek-chat" {
		t.Fatalf("niffler default = %+v", got)
	}
	catalog.Defaults["niffler"] = modelcatalog.HarnessDefault{Provider: "openai", Model: "gpt-5.4"}
	provider := catalog.Providers["openai"]
	provider.Harnesses = map[string]modelcatalog.ProviderHarness{"niffler": {ID: "proxy", BaseURL: "https://proxy.example/v1", APIKeyEnv: "PROXY_API_KEY"}}
	catalog.Providers["openai"] = provider
	got = resolveModelForTask(catalog, &runnerv1.Task{Harness: "niffler"})
	if got.ProviderID != "proxy" || got.ModelID != "gpt-5.4" || got.ProviderBaseURL != "https://proxy.example/v1" || got.ProviderAPIKeyEnv != "PROXY_API_KEY" {
		t.Fatalf("niffler override = %+v", got)
	}
}
