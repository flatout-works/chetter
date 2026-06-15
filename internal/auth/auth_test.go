package auth

import (
	"context"
	"testing"
)

func TestWithScopeAndGetScope(t *testing.T) {
	ctx := context.Background()

	t.Run("team scope round-trip", func(t *testing.T) {
		s := Scope{TeamID: "team-abc"}
		ctx = WithScope(ctx, s)
		got, ok := GetScope(ctx)
		if !ok {
			t.Fatal("GetScope returned ok=false, want true")
		}
		if got != s {
			t.Errorf("GetScope returned %+v, want %+v", got, s)
		}
	})

	t.Run("admin scope round-trip", func(t *testing.T) {
		s := Scope{Admin: true}
		ctx := WithScope(context.Background(), s)
		got, ok := GetScope(ctx)
		if !ok {
			t.Fatal("GetScope returned ok=false for admin scope")
		}
		if got != s {
			t.Errorf("GetScope returned %+v for admin scope, want %+v", got, s)
		}
	})

	t.Run("scope with both fields", func(t *testing.T) {
		s := Scope{TeamID: "team-xyz", Admin: false}
		ctx := WithScope(context.Background(), s)
		got, ok := GetScope(ctx)
		if !ok {
			t.Fatal("GetScope returned ok=false")
		}
		if got.TeamID != "team-xyz" {
			t.Errorf("TeamID = %q, want %q", got.TeamID, "team-xyz")
		}
		if got.Admin {
			t.Errorf("Admin = true, want false")
		}
	})
}

func TestGetScopeNotFound(t *testing.T) {
	_, ok := GetScope(context.Background())
	if ok {
		t.Error("GetScope on bare context returned ok=true, want false")
	}
}

func TestGetScopeFromDerivedContext(t *testing.T) {
	parent := WithScope(context.Background(), Scope{TeamID: "t1"})
	derived, cancel := context.WithCancel(parent)
	defer cancel()
	got, ok := GetScope(derived)
	if !ok {
		t.Fatal("GetScope on derived context returned ok=false")
	}
	if got.TeamID != "t1" {
		t.Errorf("GetScope on derived context returned TeamID=%q, want %q", got.TeamID, "t1")
	}
}

func TestScopeIsNotShared(t *testing.T) {
	ctxA := WithScope(context.Background(), Scope{TeamID: "a"})
	ctxB := WithScope(context.Background(), Scope{TeamID: "b"})
	gotA, _ := GetScope(ctxA)
	gotB, _ := GetScope(ctxB)
	if gotA.TeamID == gotB.TeamID {
		t.Error("scopes leaked between independent contexts")
	}
}
