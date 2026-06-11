package controller

import (
	"os"
	"strings"
)

func envValue(env map[string]string, key, fallback string) string {
	if env != nil {
		if value := strings.TrimSpace(env[key]); value != "" {
			return value
		}
	}
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
