package executil

import (
	"context"
	"fmt"
	"os/exec"
)

func Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %v: %w", name, args, err)
	}
	return string(out), nil
}

func RunIgnore(ctx context.Context, name string, args ...string) string {
	out, _ := Run(ctx, name, args...)
	return out
}
