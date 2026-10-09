package definitions

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The fixtures under testdata/triggers are verbatim copies of trigger
// definitions from the chetter-config repository, one per scope and feature
// combination. They are vendored so the round-trip guarantees are enforced in
// CI, where the sibling chetter-config checkout is not available. When a
// fixture is refreshed, copy the file unchanged: the tests rely on the original
// header comments to verify per-scope depth.
var fixtureScopes = map[string]string{
	"global": TriggerScopeGlobal,
	"groups": TriggerScopeTeam,
	"repos":  TriggerScopeRepo,
}

func fixtureFiles(t *testing.T) []struct {
	path  string
	scope string
} {
	t.Helper()
	root := filepath.Join("testdata", "triggers")
	var out []struct {
		path  string
		scope string
	}
	for dir, scope := range fixtureScopes {
		entries, err := os.ReadDir(filepath.Join(root, dir))
		if err != nil {
			t.Fatalf("read fixtures %s: %v", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
				continue
			}
			out = append(out, struct {
				path  string
				scope string
			}{filepath.Join(root, dir, entry.Name()), scope})
		}
	}
	if len(out) == 0 {
		t.Fatal("no trigger fixtures found")
	}
	return out
}

// TestRenderFixturesRoundTrip is the CI-enforced form of the corpus round-trip:
// every vendored fixture must parse, render, and re-parse to the same
// definition.
func TestRenderFixturesRoundTrip(t *testing.T) {
	for _, fixture := range fixtureFiles(t) {
		t.Run(fixture.path, func(t *testing.T) {
			raw, err := os.ReadFile(fixture.path)
			if err != nil {
				t.Fatal(err)
			}
			first, err := ParseTriggerYAML(string(raw))
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			rendered, err := RenderTriggerYAML(first, fixture.scope)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			second, err := ParseTriggerYAML(rendered)
			if err != nil {
				t.Fatalf("re-parse rendered output: %v\n--- rendered ---\n%s", err, rendered)
			}
			if !reflect.DeepEqual(first, second) {
				t.Errorf("semantic round-trip changed the definition\n--- rendered ---\n%s", rendered)
			}
		})
	}
}

// TestRenderFixtureSchemaHeaderDepth verifies the recomputed $schema comment
// depth against the real files: the fixture's own first line is what a
// hand-authored definition in that scope looks like, so the renderer must
// reproduce it exactly.
func TestRenderFixtureSchemaHeaderDepth(t *testing.T) {
	for _, fixture := range fixtureFiles(t) {
		t.Run(fixture.path, func(t *testing.T) {
			raw, err := os.ReadFile(fixture.path)
			if err != nil {
				t.Fatal(err)
			}
			sourceHeader := strings.SplitN(string(raw), "\n", 2)[0]
			if !strings.HasPrefix(sourceHeader, "# yaml-language-server: $schema=") {
				t.Fatalf("fixture is missing its schema header: %q", sourceHeader)
			}
			td, err := ParseTriggerYAML(string(raw))
			if err != nil {
				t.Fatal(err)
			}
			rendered, err := RenderTriggerYAML(td, fixture.scope)
			if err != nil {
				t.Fatal(err)
			}
			renderedHeader := strings.SplitN(rendered, "\n", 2)[0]
			if renderedHeader != sourceHeader {
				t.Errorf("schema header depth wrong for scope %q:\n got %q\nwant %q",
					fixture.scope, renderedHeader, sourceHeader)
			}
		})
	}
}

// TestRenderFixturesPreserveIdentity asserts no fixture loses identity or
// resource fields through the renderer's omit-empty rules.
func TestRenderFixturesPreserveIdentity(t *testing.T) {
	for _, fixture := range fixtureFiles(t) {
		raw, err := os.ReadFile(fixture.path)
		if err != nil {
			t.Fatal(err)
		}
		src, err := ParseTriggerYAML(string(raw))
		if err != nil {
			t.Fatal(err)
		}
		rendered, err := RenderTriggerYAML(src, fixture.scope)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseTriggerYAML(rendered)
		if err != nil {
			t.Fatal(err)
		}
		if src.Name == "" {
			t.Errorf("%s: fixture has no name", fixture.path)
		}
		if src.Name != got.Name {
			t.Errorf("%s: name changed %q -> %q", fixture.path, src.Name, got.Name)
		}
		if src.Enabled != got.Enabled {
			t.Errorf("%s: enabled changed", fixture.path)
		}
		if !reflect.DeepEqual(src.Skills, got.Skills) {
			t.Errorf("%s: skills changed %v -> %v", fixture.path, src.Skills, got.Skills)
		}
	}
}
