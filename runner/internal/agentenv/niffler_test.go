package agentenv

import (
	"github.com/flatout-works/chetter/runner/internal/task"
	"testing"
)

func TestNifflerControlEnvironment(t *testing.T) {
	req := task.TaskRequest{Harness: "niffler"}
	for _, key := range []string{"NIF_ROOT", "NIF_NATS_URL", "NIF_LLM_PROVIDERS", "CHETTER_NIFFLER_PROXY_TOKEN"} {
		if !IsManagedEnv(key, req) {
			t.Errorf("unmanaged %s", key)
		}
		t.Setenv(key, "value")
		if e := ValidateEndpointTokenEnvironment([]task.MCPEndpoint{{BearerTokenEnv: key}}); e == nil {
			t.Errorf("endpoint can override %s", key)
		}
	}
	if IsManagedEnv("CUSTOM_ENV", req) {
		t.Fatal("custom env reserved")
	}
}
