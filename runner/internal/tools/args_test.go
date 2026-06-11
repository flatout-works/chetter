package tools

import "testing"

func TestGetString(t *testing.T) {
	t.Run("key exists with string value", func(t *testing.T) {
		args := map[string]any{"name": "alice"}
		v, err := getString(args, "name")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v != "alice" {
			t.Fatalf("expected alice, got %q", v)
		}
	})
	t.Run("key exists with non-string value", func(t *testing.T) {
		args := map[string]any{"count": 42}
		_, err := getString(args, "count")
		if err == nil {
			t.Fatal("expected error for non-string value")
		}
	})
	t.Run("key missing", func(t *testing.T) {
		args := map[string]any{}
		_, err := getString(args, "missing")
		if err == nil {
			t.Fatal("expected error for missing key")
		}
	})
}

func TestRequireString(t *testing.T) {
	t.Run("key exists with non-empty string", func(t *testing.T) {
		args := map[string]any{"name": "alice"}
		v, err := requireString(args, "name")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v != "alice" {
			t.Fatalf("expected alice, got %q", v)
		}
	})
	t.Run("key exists with empty string", func(t *testing.T) {
		args := map[string]any{"name": ""}
		_, err := requireString(args, "name")
		if err == nil {
			t.Fatal("expected error for empty string")
		}
	})
	t.Run("key missing", func(t *testing.T) {
		args := map[string]any{}
		_, err := requireString(args, "name")
		if err == nil {
			t.Fatal("expected error for missing key")
		}
	})
}

func TestGetOptString(t *testing.T) {
	t.Run("key exists with string", func(t *testing.T) {
		args := map[string]any{"name": "alice"}
		v := getOptString(args, "name", "default")
		if v != "alice" {
			t.Fatalf("expected alice, got %q", v)
		}
	})
	t.Run("key missing", func(t *testing.T) {
		args := map[string]any{}
		v := getOptString(args, "name", "default")
		if v != "default" {
			t.Fatalf("expected default, got %q", v)
		}
	})
	t.Run("key exists with non-string", func(t *testing.T) {
		args := map[string]any{"name": 42}
		v := getOptString(args, "name", "default")
		if v != "default" {
			t.Fatalf("expected default for non-string, got %q", v)
		}
	})
}

func TestGetOptFloat64(t *testing.T) {
	t.Run("key exists with float64", func(t *testing.T) {
		args := map[string]any{"rate": 3.14}
		v := getOptFloat64(args, "rate", 1.0)
		if v != 3.14 {
			t.Fatalf("expected 3.14, got %v", v)
		}
	})
	t.Run("key missing", func(t *testing.T) {
		args := map[string]any{}
		v := getOptFloat64(args, "rate", 1.0)
		if v != 1.0 {
			t.Fatalf("expected 1.0, got %v", v)
		}
	})
	t.Run("key exists with non-float64", func(t *testing.T) {
		args := map[string]any{"rate": "fast"}
		v := getOptFloat64(args, "rate", 1.0)
		if v != 1.0 {
			t.Fatalf("expected default for non-float64, got %v", v)
		}
	})
}

func TestGetOptBool(t *testing.T) {
	t.Run("key exists with bool", func(t *testing.T) {
		args := map[string]any{"verbose": true}
		v := getOptBool(args, "verbose", false)
		if v != true {
			t.Fatalf("expected true, got %v", v)
		}
	})
	t.Run("key missing", func(t *testing.T) {
		args := map[string]any{}
		v := getOptBool(args, "verbose", false)
		if v != false {
			t.Fatalf("expected false, got %v", v)
		}
	})
	t.Run("key exists with non-bool", func(t *testing.T) {
		args := map[string]any{"verbose": "yes"}
		v := getOptBool(args, "verbose", true)
		if v != true {
			t.Fatalf("expected default for non-bool, got %v", v)
		}
	})
}

func TestGetOptStringMap(t *testing.T) {
	t.Run("key exists with map", func(t *testing.T) {
		m := map[string]any{"FOO": "bar"}
		args := map[string]any{"env": m}
		v := getOptStringMap(args, "env")
		if v["FOO"] != "bar" {
			t.Fatalf("expected bar, got %v", v["FOO"])
		}
	})
	t.Run("key missing", func(t *testing.T) {
		args := map[string]any{}
		v := getOptStringMap(args, "env")
		if v != nil {
			t.Fatalf("expected nil, got %v", v)
		}
	})
	t.Run("key exists with non-map", func(t *testing.T) {
		args := map[string]any{"env": "notamap"}
		v := getOptStringMap(args, "env")
		if v != nil {
			t.Fatalf("expected nil for non-map, got %v", v)
		}
	})
}

func TestGetOptStringSlice(t *testing.T) {
	t.Run("key exists with slice", func(t *testing.T) {
		s := []any{"a", "b"}
		args := map[string]any{"items": s}
		v := getOptStringSlice(args, "items")
		if len(v) != 2 || v[0] != "a" || v[1] != "b" {
			t.Fatalf("expected [a b], got %v", v)
		}
	})
	t.Run("key missing", func(t *testing.T) {
		args := map[string]any{}
		v := getOptStringSlice(args, "items")
		if v != nil {
			t.Fatalf("expected nil, got %v", v)
		}
	})
	t.Run("key exists with non-slice", func(t *testing.T) {
		args := map[string]any{"items": "notaslice"}
		v := getOptStringSlice(args, "items")
		if v != nil {
			t.Fatalf("expected nil for non-slice, got %v", v)
		}
	})
}
