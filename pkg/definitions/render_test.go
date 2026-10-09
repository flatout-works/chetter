package definitions

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// corpusDir returns the path to a chetter-config checkout, or "" when it is not
// available. The corpus tests are skipped rather than failed when the sibling
// checkout is absent (for example in CI without the config repo), because the
// renderer's guarantees also hold on the synthetic cases below.
func corpusDir(t *testing.T) string {
	t.Helper()
	candidates := []string{}
	if env := os.Getenv("CHETTER_CONFIG_DIR"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates,
		filepath.Join("..", "..", "..", "chetter-config"),
		filepath.Join("..", "..", "chetter-config"),
	)
	for _, dir := range candidates {
		if info, err := os.Stat(filepath.Join(dir, "global", "triggers")); err == nil && info.IsDir() {
			return dir
		}
	}
	return ""
}

// triggerFilesFrom walks every trigger definition in a chetter-config checkout,
// returning path plus the scope the file lives in.
func triggerFilesFrom(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml") {
			return nil
		}
		if filepath.Base(filepath.Dir(path)) != "triggers" {
			return nil
		}
		out = append(out, path)
		return nil
	})
	if err != nil {
		t.Fatalf("walk corpus: %v", err)
	}
	return out
}

// scopeForPath derives the definition scope from a repo-relative path.
func scopeForPath(t *testing.T, rel string) string {
	t.Helper()
	parts := strings.Split(filepath.ToSlash(rel), "/")
	switch {
	case len(parts) > 0 && parts[0] == "global":
		return TriggerScopeGlobal
	case len(parts) > 1 && parts[0] == "groups":
		return TriggerScopeTeam
	case len(parts) > 1 && parts[0] == "repos":
		return TriggerScopeRepo
	default:
		t.Fatalf("cannot derive scope from %q", rel)
		return ""
	}
}

// TestRenderRoundTripSemantic is the core Phase 1 guarantee: for every trigger
// definition in the corpus, parsing, rendering, and re-parsing yields the same
// definition. Byte-identity with the source file is NOT asserted, because the
// corpus is not canonically ordered or quoted and its free-text comments are
// dropped by the parser.
func TestRenderRoundTripSemantic(t *testing.T) {
	root := corpusDir(t)
	if root == "" {
		t.Skip("no chetter-config checkout available")
	}
	files := triggerFilesFrom(t, root)
	if len(files) == 0 {
		t.Fatal("no trigger definitions found in corpus")
	}
	for _, path := range files {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatal(err)
		}
		t.Run(rel, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			first, err := ParseTriggerYAML(string(raw))
			if err != nil {
				t.Fatalf("parse source: %v", err)
			}
			rendered, err := RenderTriggerYAML(first, scopeForPath(t, rel))
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			second, err := ParseTriggerYAML(rendered)
			if err != nil {
				t.Fatalf("re-parse rendered output: %v\n--- rendered ---\n%s", err, rendered)
			}
			if !reflect.DeepEqual(first, second) {
				t.Errorf("semantic round-trip changed the definition\n--- rendered ---\n%s\n--- before ---\n%+v\n--- after ---\n%+v",
					rendered, first, second)
			}
		})
	}
}

// TestRenderIsByteStable asserts repeated renders of the same definition are
// byte-identical, which is what makes a promotion diff trustworthy.
func TestRenderIsByteStable(t *testing.T) {
	root := corpusDir(t)
	if root == "" {
		t.Skip("no chetter-config checkout available")
	}
	files := triggerFilesFrom(t, root)
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		td, err := ParseTriggerYAML(string(raw))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		scope := scopeForPath(t, rel)
		first, err := RenderTriggerYAML(td, scope)
		if err != nil {
			t.Fatal(err)
		}
		second, err := RenderTriggerYAML(td, scope)
		if err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Errorf("%s: render is not deterministic", rel)
		}
	}
}

// TestRenderPreservesCorpusFields spot-checks that every field present in a
// source definition survives the round trip, rather than being silently dropped
// by the renderer's omit-empty rules.
func TestRenderPreservesCorpusFields(t *testing.T) {
	root := corpusDir(t)
	if root == "" {
		t.Skip("no chetter-config checkout available")
	}
	for _, path := range triggerFilesFrom(t, root) {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		src, err := ParseTriggerYAML(string(raw))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		rendered, err := RenderTriggerYAML(src, scopeForPath(t, rel))
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseTriggerYAML(rendered)
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if src.Name != got.Name || src.Enabled != got.Enabled || src.Harness != got.Harness {
			t.Errorf("%s: identity fields changed: %+v -> %+v", rel, src, got)
		}
		if src.TimeoutSec != got.TimeoutSec {
			t.Errorf("%s: timeout_sec changed: %d -> %d", rel, src.TimeoutSec, got.TimeoutSec)
		}
		if !reflect.DeepEqual(src.Skills, got.Skills) {
			t.Errorf("%s: skills changed: %v -> %v", rel, src.Skills, got.Skills)
		}
	}
}

func TestTriggerSchemaCommentPrefix(t *testing.T) {
	cases := map[string]string{
		TriggerScopeGlobal: "../../../",
		TriggerScopeTeam:   "../../../../",
		TriggerScopeRepo:   "../../../../../",
	}
	for scope, want := range cases {
		if got := TriggerSchemaCommentPrefix(scope); got != want {
			t.Errorf("scope %q: prefix = %q, want %q", scope, got, want)
		}
		if !strings.HasPrefix(TriggerSchemaComment(scope), "# yaml-language-server: $schema=") {
			t.Errorf("scope %q: malformed comment %q", scope, TriggerSchemaComment(scope))
		}
	}
}

// TestUnfoldTriggerConfig covers the flat-key inversion, the second of the
// three transforms the renderer undoes.
func TestUnfoldTriggerConfig(t *testing.T) {
	cases := []struct {
		name      string
		cfg       string
		wantFlat  map[string]string
		wantRest  string
		expectErr bool
	}{
		{
			name:     "empty",
			cfg:      "",
			wantFlat: map[string]string{},
		},
		{
			name:     "empty object",
			cfg:      "{}",
			wantFlat: map[string]string{},
		},
		{
			name:     "flat keys extracted",
			cfg:      `{"session_mode":"resumable","ttl_hours":24,"pause_reason":"deploy"}`,
			wantFlat: map[string]string{"session_mode": "resumable", "ttl_hours": "24", "pause_reason": "deploy"},
		},
		{
			name:     "match labels",
			cfg:      `{"match_labels":["enhancement","bug"]}`,
			wantFlat: map[string]string{"match_labels": `["enhancement","bug"]`},
		},
		{
			name:     "unknown keys remain",
			cfg:      `{"session_mode":"resumable","future_option":true}`,
			wantFlat: map[string]string{"session_mode": "resumable"},
			wantRest: `{"future_option":true}`,
		},
		{
			name:      "invalid json",
			cfg:       `{`,
			expectErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flat, rest, err := unfoldTriggerConfig(tc.cfg)
			if tc.expectErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(flat, tc.wantFlat) {
				t.Errorf("flat = %v, want %v", flat, tc.wantFlat)
			}
			if rest != tc.wantRest {
				t.Errorf("rest = %q, want %q", rest, tc.wantRest)
			}
		})
	}
}

// TestRenderUnfoldsFlatKeys asserts the end-to-end shape: a definition whose
// config is stored as a flattened blob renders back to top-level keys.
func TestRenderUnfoldsFlatKeys(t *testing.T) {
	td := TriggerDef{
		Name:        "example",
		Enabled:     true,
		CronExpr:    "0 0 * * *",
		TriggerCfg:  `{"session_mode":"resumable","ttl_hours":24}`,
		Prompt:      "Do the thing.",
		Harness:     "claude-code",
		TimeoutSec:  3600,
		TriggerType: "cron",
	}
	out, err := RenderTriggerYAML(td, TriggerScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"session_mode: resumable\n", "ttl_hours: 24\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q\n%s", want, out)
		}
	}
	// cron is the parse default and must be omitted.
	if strings.Contains(out, "trigger_type:") {
		t.Errorf("cron trigger_type should be omitted:\n%s", out)
	}
	if !strings.Contains(out, `cron_expr: "0 0 * * *"`) {
		t.Errorf("cron_expr should be quoted:\n%s", out)
	}
	got, err := ParseTriggerYAML(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if !reflect.DeepEqual(got, td) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, td)
	}
}

// TestRenderPromptBlockScalar covers the block-scalar safety rules, which
// decide whether a prompt can be emitted as `|-` or must be quoted.
func TestRenderPromptBlockScalar(t *testing.T) {
	base := TriggerDef{Name: "p", Enabled: true, TimeoutSec: 60}
	blockCases := []string{
		"single line",
		"line one\nline two",
		"has\n\nblank line inside",
	}
	for _, prompt := range blockCases {
		td := base
		td.Prompt = prompt
		out, err := RenderTriggerYAML(td, TriggerScopeGlobal)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "prompt: |-\n") {
			t.Errorf("prompt %q should use a block scalar:\n%s", prompt, out)
		}
		got, err := ParseTriggerYAML(out)
		if err != nil {
			t.Fatalf("re-parse %q: %v\n%s", prompt, err, out)
		}
		if got.Prompt != prompt {
			t.Errorf("prompt changed: %q -> %q", prompt, got.Prompt)
		}
	}
	quotedCases := []string{
		" leading space",
		"trailing newline\n",
		"has a\n\ttab indent",
		"has an\n  indented line",
	}
	for _, prompt := range quotedCases {
		td := base
		td.Prompt = prompt
		out, err := RenderTriggerYAML(td, TriggerScopeGlobal)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(out, "prompt: |-\n") {
			t.Errorf("prompt %q must not use a block scalar:\n%s", prompt, out)
		}
		got, err := ParseTriggerYAML(out)
		if err != nil {
			t.Fatalf("re-parse %q: %v\n%s", prompt, err, out)
		}
		if got.Prompt != prompt {
			t.Errorf("prompt changed: %q -> %q", prompt, got.Prompt)
		}
	}
}

// TestRenderOmitsEmptyFields guards the omit-empty rules that keep a promoted
// file free of empty keys.
func TestRenderOmitsEmptyFields(t *testing.T) {
	td := TriggerDef{Name: "bare", Enabled: true, TimeoutSec: 60}
	out, err := RenderTriggerYAML(td, TriggerScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	for _, unwanted := range []string{"cron_expr:", "agent_image:", "git_url:", "skills:", "session_mode:", "prompt:"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("rendered output should omit %q:\n%s", unwanted, out)
		}
	}
	for _, wanted := range []string{"name: bare\n", "enabled: true\n", "timeout_sec: 60\n"} {
		if !strings.Contains(out, wanted) {
			t.Errorf("rendered output missing %q:\n%s", wanted, out)
		}
	}
}

func TestRenderRejectsEmptyName(t *testing.T) {
	if _, err := RenderTriggerYAML(TriggerDef{}, TriggerScopeGlobal); err == nil {
		t.Fatal("expected an error for an empty name")
	}
}

// TestRenderAdoptOptIn covers the H1 escape hatch round-tripping: a definition
// that opts in to adopting a database-created trigger must keep that opt-in
// through parse and render, and a definition without it must not gain one.
func TestRenderAdoptOptIn(t *testing.T) {
	withAdopt := TriggerDef{Name: "adopter", Enabled: true, TimeoutSec: 60, Adopt: true}
	out, err := RenderTriggerYAML(withAdopt, TriggerScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "adopt: true\n") {
		t.Errorf("adopt: true was not rendered:\n%s", out)
	}
	got, err := ParseTriggerYAML(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if !got.Adopt {
		t.Error("adopt did not survive the round trip")
	}

	withoutAdopt := TriggerDef{Name: "plain", Enabled: true, TimeoutSec: 60}
	out, err = RenderTriggerYAML(withoutAdopt, TriggerScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "adopt") {
		t.Errorf("adopt must not be emitted when unset:\n%s", out)
	}
}

// TestParseTriggerYAMLRejectsUnknownFields guards the KnownFields decoder:
// a typo in a definition must fail the sync rather than be silently ignored.
func TestParseTriggerYAMLRejectsUnknownFields(t *testing.T) {
	if _, err := ParseTriggerYAML("name: x\nadopt_typo: true\n"); err == nil {
		t.Fatal("an unknown trigger field should be rejected")
	}
}

// TestRenderTaskDescriptorMatchesParserConfigKeys fails if ParseTriggerYAML
// learns a new flat key that the renderer does not unfold, which would silently
// drop that key on promotion.
func TestRenderTaskDescriptorMatchesParserConfigKeys(t *testing.T) {
	// Parse a definition that sets every flat key, then confirm the renderer
	// reproduces all of them.
	content := strings.Join([]string{
		"name: all-keys",
		"enabled: true",
		"trigger_type: pr_review",
		"repo: owner/repo",
		"event: opened",
		"match_labels:",
		"  - a",
		"  - b",
		"timeout_sec: 60",
		"session_mode: resumable",
		"pause_reason: deploy",
		"ttl_hours: 24",
		"prompt: |-",
		"  hi",
		"",
	}, "\n")
	td, err := ParseTriggerYAML(content)
	if err != nil {
		t.Fatal(err)
	}
	out, err := RenderTriggerYAML(td, TriggerScopeGlobal)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range configKeys {
		if key == "ttl_hours" || key == "match_labels" {
			continue
		}
		if !strings.Contains(out, key+": ") {
			t.Errorf("flat key %q was not unfolded into the output:\n%s", key, out)
		}
	}
	if !strings.Contains(out, "ttl_hours: 24\n") {
		t.Errorf("ttl_hours missing:\n%s", out)
	}
	if !strings.Contains(out, "match_labels:\n  - a\n  - b\n") {
		t.Errorf("match_labels missing or malformed:\n%s", out)
	}
}
