package definitions

import "testing"

func TestParseTriggerHarnessChoices(t *testing.T) {
	for _, name := range []string{"", "opencode", "claude-code", "pi", "codewhale", "codex", "niffler", "unknown"} {
		t.Run(name, func(t *testing.T) {
			trigger, err := ParseTriggerYAML("name: harness-test\nharness: '" + name + "'\n")
			if name == "unknown" {
				if err == nil {
					t.Fatal("unknown harness accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if trigger.Harness != name {
				t.Fatalf("harness = %q, want %q", trigger.Harness, name)
			}
		})
	}
}
