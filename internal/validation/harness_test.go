package validation

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestHarnessSchemaParity(t *testing.T) {
	want := []string{"opencode", "claude-code", "pi", "codewhale", "codex", "niffler"}
	if !reflect.DeepEqual(SupportedHarnesses, want) {
		t.Fatalf("supported harnesses = %v", SupportedHarnesses)
	}
	for _, filename := range []string{"runner", "trigger"} {
		t.Run(filename, func(t *testing.T) {
			data, err := os.ReadFile("../../schemas/" + filename + ".schema.json")
			if err != nil {
				t.Fatal(err)
			}
			var schema struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal(data, &schema); err != nil {
				t.Fatal(err)
			}
			if filename == "runner" {
				if err := json.Unmarshal(schema.Properties["execution"], &schema); err != nil {
					t.Fatal(err)
				}
			}
			var harness struct {
				Enum []string `json:"enum"`
			}
			if err := json.Unmarshal(schema.Properties["harness"], &harness); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(harness.Enum, want) {
				t.Fatalf("schema harnesses = %v, want %v", harness.Enum, want)
			}
		})
	}
}
