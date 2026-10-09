package service

import (
	"context"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/internal/auth"
)

// TestCreateTeamRejectsInvalidNames exercises the create path's team-name
// validation. Invalid names are rejected before the service touches the
// repository, so a zero-value Service is enough here.
func TestCreateTeamRejectsInvalidNames(t *testing.T) {
	svc := &Service{}
	ctx := auth.WithScope(context.Background(), auth.Scope{Admin: true})

	invalid := []string{
		"Chetter Core",
		"ChetterCore",
		"chetter_core",
		"chetter core",
		"-chetter",
		"chetter-",
		"a--b",
		"chetter/core",
		"chetter.core",
		"..",
		"",
	}
	for _, name := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.CreateTeam(ctx, name); err == nil {
				t.Fatalf("CreateTeam(%q) = nil error, want rejection", name)
			} else if !strings.Contains(err.Error(), "lowercase-with-dashes") && name != "" {
				t.Fatalf("CreateTeam(%q) error %q does not name the rule", name, err)
			}
		})
	}
}

// TestCreateTeamRequiresAdmin documents that the name rule is checked after
// the admin guard.
func TestCreateTeamRequiresAdmin(t *testing.T) {
	svc := &Service{}
	if _, err := svc.CreateTeam(context.Background(), "chetter-core"); err == nil || !strings.Contains(err.Error(), "admin access required") {
		t.Fatalf("expected admin access error, got %v", err)
	}
}
