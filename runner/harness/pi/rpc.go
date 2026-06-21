package pi

import (
	"os"
	"strings"

	"github.com/flatout-works/chetter/runner/internal/task"
)

func buildRPCCommand(req task.TaskRequest) []string {
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
	if model == "" {
		model = strings.TrimSpace(os.Getenv("PI_MODEL"))
	}
	if provider == "" {
		provider = strings.TrimSpace(os.Getenv("PI_PROVIDER"))
	}
	if provider == "" && strings.Contains(model, "/") {
		parts := strings.SplitN(model, "/", 2)
		provider = parts[0]
		model = parts[1]
	}
	if model == "" {
		model = "glm-5.2"
	}
	if provider == "" {
		provider = "zai"
	}
	return provider, model
}

func thinkingLevel(variant string) string {
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case "off", "minimal", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(variant))
	default:
		return ""
	}
}
