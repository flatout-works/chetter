package teamname

import (
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	valid := []string{
		"chetter-core",
		"core",
		"a1-b2",
		"a",
		"0",
		"team-123",
	}
	for _, name := range valid {
		if err := Validate(name); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", name, err)
		}
		if !IsValid(name) {
			t.Errorf("IsValid(%q) = false, want true", name)
		}
	}

	invalid := []string{
		"Chetter Core",
		"ChetterCore",
		"chetter_core",
		"chetter core",
		"-chetter",
		"chetter-",
		"a--b",
		"a/b",
		"..",
		".",
		"chetter.core",
		"Cheter",
		"chetter ",
		" chetter",
		"",
		"café",
	}
	for _, name := range invalid {
		if err := Validate(name); err == nil {
			t.Errorf("Validate(%q) = nil, want error", name)
		}
		if IsValid(name) {
			t.Errorf("IsValid(%q) = true, want false", name)
		}
	}
}

func TestValidateErrorsNameTheRule(t *testing.T) {
	err := Validate("Chetter Core")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "lowercase-with-dashes") {
		t.Fatalf("error %q does not name the rule", err)
	}
	if !strings.Contains(err.Error(), "Chetter Core") {
		t.Fatalf("error %q does not name the offending value", err)
	}
}

func TestValidateRejectsOverlongName(t *testing.T) {
	if err := Validate(strings.Repeat("a", MaxLen+1)); err == nil {
		t.Fatal("expected an over-long name to be rejected")
	}
	if err := Validate(strings.Repeat("a", MaxLen)); err != nil {
		t.Fatalf("a name of exactly MaxLen must be accepted: %v", err)
	}
}
