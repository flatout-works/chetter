package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/pkg/definitions"
)

func TestTriggerDefinitionPath(t *testing.T) {
	cases := []struct {
		name       string
		scope      string
		team       string
		repo       string
		trigger    string
		want       string
		wantErrSub string
	}{
		{
			name: "global", scope: definitions.TriggerScopeGlobal, trigger: "nightly",
			want: "global/triggers/nightly.yaml",
		},
		{
			name: "team", scope: definitions.TriggerScopeTeam, team: "chetter-core", trigger: "nightly",
			want: "groups/chetter-core/triggers/nightly.yaml",
		},
		{
			name: "repo", scope: definitions.TriggerScopeRepo, repo: "gokr/niffler", trigger: "nightly",
			want: "repos/gokr/niffler/triggers/nightly.yaml",
		},
		{
			name: "team requires a name", scope: definitions.TriggerScopeTeam, trigger: "nightly",
			wantErrSub: "team_name is required",
		},
		{
			name: "repo requires owner/repo", scope: definitions.TriggerScopeRepo, repo: "nogslash", trigger: "nightly",
			wantErrSub: "must be owner/repo",
		},
		{
			name: "repo rejects nested path", scope: definitions.TriggerScopeRepo, repo: "a/b/c", trigger: "nightly",
			wantErrSub: "must be owner/repo",
		},
		{
			name: "unknown scope", scope: "universe", trigger: "nightly",
			wantErrSub: "scope must be",
		},
		{
			name: "name cannot traverse", scope: definitions.TriggerScopeGlobal, trigger: "../escape",
			wantErrSub: "cannot be used as a file name",
		},
		{
			name: "team cannot traverse", scope: definitions.TriggerScopeTeam, team: "../up", trigger: "nightly",
			wantErrSub: "cannot be used as a directory name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := triggerDefinitionPath(tc.scope, tc.team, tc.repo, tc.trigger)
			if tc.wantErrSub != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q", tc.wantErrSub)
				}
				if !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("error %q does not contain %q", err, tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("path = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScanRenderedTriggerForSecrets(t *testing.T) {
	clean := "name: x\nadopt: true\nprompt: |-\n  Use $MY_API_KEY from the environment.\n"
	if hits := scanRenderedTriggerForSecrets(clean); len(hits) != 0 {
		t.Errorf("clean content flagged: %v", hits)
	}
	// Env var *names* are the sanctioned pattern and must not be flagged.
	envNames := "name: x\nenv:\n  API_KEY_ENV: SYNTHETIC_API_KEY\napi_key_env: DEEPSEEK_API_KEY\n"
	if hits := scanRenderedTriggerForSecrets(envNames); len(hits) != 0 {
		t.Errorf("env var names should not be flagged: %v", hits)
	}
	// But a literal credential value under a key without the _ENV convention
	// still must be.
	if hits := scanRenderedTriggerForSecrets("token: sk-live-abcdef1234567890\n"); len(hits) == 0 {
		t.Error("a literal credential value should still be flagged")
	}
	dirty := []struct {
		name    string
		content string
	}{
		{"assignment", "prompt: |-\n  export SYNTHETIC_API_KEY=sk-live-abcdef1234567890\n"},
		{"json-ish", `prompt: |-\n  {"token": "ghp_abcdefghijklmnopqrstuvwxyz0123"}` + "\n"},
		{"bearer", "prompt: |-\n  Authorization: Bearer abcdefghijklmnopqrstuvwxyz\n"},
		{"pem", "prompt: |-\n  -----BEGIN RSA PRIVATE KEY-----\n"},
		{"password", "prompt: |-\n  password: hunter2hunter2\n"},
	}
	for _, tc := range dirty {
		t.Run(tc.name, func(t *testing.T) {
			hits := scanRenderedTriggerForSecrets(tc.content)
			if len(hits) == 0 {
				t.Errorf("credential-shaped content was not flagged:\n%s", tc.content)
			}
		})
	}
}

// TestPromoteTriggerRendersCanonicalYAML is the end-to-end render check: a
// draft created through the API renders to a definition that parses back to the
// same trigger, under the canonical shape.
func TestPromoteTriggerRendersCanonicalYAML(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	rec, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:        "nightly-reindex",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "0 3 * * *",
		Prompt:      "Reindex the search corpus.",
		AgentImage:  "golang",
		Agent:       "docs-maintainer",
		Harness:     "claude-code",
		TimeoutSec:  3600,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	if rec.Name != "nightly-reindex" {
		t.Fatalf("unexpected trigger: %+v", rec)
	}

	out, err := svc.PromoteTrigger(ctx, PromoteTriggerInput{
		Name:   "nightly-reindex",
		Scope:  definitions.TriggerScopeGlobal,
		DryRun: true,
	})
	if err != nil {
		t.Fatalf("promote (dry run): %v", err)
	}
	if out.Path != "global/triggers/nightly-reindex.yaml" {
		t.Errorf("path = %q", out.Path)
	}
	if !out.DryRun {
		t.Error("dry_run should be reported")
	}
	if out.Proposal != nil {
		t.Error("a dry run must not create a proposal")
	}

	// The rendered content must carry the per-scope schema header and be a
	// valid definition that parses back to what we started from.
	if !strings.HasPrefix(out.Content, "# yaml-language-server: $schema=../../../") {
		t.Errorf("missing global-scope schema header:\n%s", out.Content)
	}
	parsed, err := definitions.ParseTriggerYAML(out.Content)
	if err != nil {
		t.Fatalf("rendered definition does not parse: %v\n%s", err, out.Content)
	}
	if parsed.Name != "nightly-reindex" {
		t.Errorf("name = %q", parsed.Name)
	}
	if parsed.CronExpr != "0 3 * * *" {
		t.Errorf("cron_expr = %q", parsed.CronExpr)
	}
	if parsed.TimeoutSec != 3600 {
		t.Errorf("timeout_sec = %d", parsed.TimeoutSec)
	}
	if parsed.Agent != "docs-maintainer" {
		t.Errorf("agent = %q", parsed.Agent)
	}
	// agent_image is stored resolved; the definition keeps the short name.
	if parsed.AgentImage != "golang" {
		t.Errorf("agent_image = %q, want the authored short name", parsed.AgentImage)
	}
	if parsed.Adopt {
		t.Error("a promotion must not silently set adopt: true")
	}
}

// TestPromoteTriggerRefusesManagedTrigger verifies the guard against promoting
// something Git already owns.
func TestPromoteTriggerRefusesManagedTrigger(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	// Materialize a Git-managed trigger through the real sync path so the guard
	// is exercised against a genuine source_id.
	repoDir := createTriggerDefinitionsRepo(t, []triggerFile{
		{path: "global/triggers/already-managed.yaml", content: cronTriggerYAML("already-managed", "0 0 * * *", true)},
	})
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))
	if _, err := svc.SyncDefinitions(ctx); err != nil {
		t.Fatalf("sync managed trigger: %v", err)
	}

	_, err := svc.PromoteTrigger(ctx, PromoteTriggerInput{Name: "already-managed"})
	if err == nil {
		t.Fatal("promoting a Git-managed trigger should be refused")
	}
	if !strings.Contains(err.Error(), "already managed") {
		t.Errorf("error should explain the ownership, got: %v", err)
	}
}

// TestPromoteTriggerRefusesSecretContent verifies the secret scan blocks a PR
// while allow_secret overrides it with a recorded warning.
func TestPromoteTriggerRefusesSecretContent(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	_, err := svc.CreateTrigger(ctx, store.TriggerInput{
		Name:        "leaky",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "0 4 * * *",
		Prompt:      "Call the API with SYNTHETIC_API_KEY=sk-live-abcdef1234567890 please.",
		AgentImage:  "golang",
		TimeoutSec:  60,
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	_, err = svc.PromoteTrigger(ctx, PromoteTriggerInput{Name: "leaky", DryRun: true})
	if err == nil {
		t.Fatal("credential-shaped content should be refused")
	}
	if !strings.Contains(err.Error(), "allow_secret") {
		t.Errorf("refusal should name the override, got: %v", err)
	}

	out, err := svc.PromoteTrigger(ctx, PromoteTriggerInput{Name: "leaky", DryRun: true, AllowSecret: true})
	if err != nil {
		t.Fatalf("allow_secret should permit the dry run: %v", err)
	}
	if len(out.Warnings) == 0 {
		t.Error("overriding the secret scan should record a warning")
	}
}

func TestPromoteTriggerRejectsUnknownTrigger(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	if _, err := svc.PromoteTrigger(context.Background(), PromoteTriggerInput{Name: "does-not-exist"}); err == nil {
		t.Fatal("promoting a missing trigger should fail")
	}
}

func TestUnresolveAgentImageRef(t *testing.T) {
	const prefix = "ghcr.io/flatout-works/chetter-agent"
	cases := []struct{ in, want string }{
		{"ghcr.io/flatout-works/chetter-agent/golang", "golang"},
		{"golang", "golang"},
		{"", ""},
		{"other.registry/x/image", "other.registry/x/image"},
	}
	for _, tc := range cases {
		if got := unresolveAgentImageRef(tc.in, prefix); got != tc.want {
			t.Errorf("unresolveAgentImageRef(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
