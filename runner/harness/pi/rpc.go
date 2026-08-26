package pi

import (
	"os"
	"strings"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func buildRPCCommand(req task.TaskRequest) []string {
	// --approve is pi's project-trust override (projectTrustOverride): it loads
	// the project's .pi/ extensions and skills without a trust prompt. It is
	// NOT a tool-approval bypass — pi has no built-in permission system, and in
	// headless RPC mode interactive extension UI requests are auto-answered
	// with cancelled:true by the runner. Safe for untrusted repositories
	// because gVisor is the actual task boundary.
	args := []string{"pi", "--mode", "rpc", "--no-session", "--offline", "--approve"}
	provider, model := modelFields(req)
	if provider != "" {
		args = append(args, "--provider", provider)
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	if thinking := thinkingLevel(req.VariantID); thinking != "" {
		args = append(args, "--thinking", thinking)
	}
	return args
}

func resolvedModelID(req task.TaskRequest) string {
	provider, model := modelFields(req)
	if provider == "" {
		return model
	}
	if model == "" {
		return provider
	}
	if strings.Contains(model, "/") {
		return model
	}
	return provider + "/" + model
}

func modelFields(req task.TaskRequest) (provider, model string) {
	provider = strings.TrimSpace(req.ProviderID)
	model = strings.TrimSpace(req.ModelID)

	// A provider embedded in an explicitly requested model takes precedence
	// over ambient defaults. An explicitly separate ProviderID remains the
	// highest-precedence setting.
	if provider == "" && model != "" {
		provider, model = splitQualifiedModel(model)
	}
	if model == "" {
		model = strings.TrimSpace(os.Getenv("PI_MODEL"))
	}
	if provider == "" {
		provider = strings.TrimSpace(os.Getenv("PI_PROVIDER"))
	}
	if provider == "" {
		provider, model = splitQualifiedModel(model)
	}
	if model == "" {
		model = "glm-5.2"
	}
	if provider == "" {
		provider = "zai"
	}
	return provider, model
}

func splitQualifiedModel(model string) (provider, unqualifiedModel string) {
	parts := strings.SplitN(model, "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", model
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func thinkingLevel(variant string) string {
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "off", "minimal", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(variant))
	default:
		return ""
	}
}
