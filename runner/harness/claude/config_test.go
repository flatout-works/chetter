package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateConfigDeniesInteractiveQuestions(t *testing.T) {
	wsDir := t.TempDir()
	if err := GenerateConfig(wsDir, "", "", "", false); err != nil {
		t.Fatalf("GenerateConfig failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(wsDir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		Permissions struct {
			Deny []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	for _, rule := range settings.Permissions.Deny {
		if rule == "AskUserQuestion" {
			return
		}
	}
	t.Fatalf("expected AskUserQuestion to be denied, got %q", settings.Permissions.Deny)
}
