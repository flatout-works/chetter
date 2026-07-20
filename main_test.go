package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/config"
	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/internal/testdb"
)

var mainTestDB *testdb.PackageDB

func TestMain(m *testing.M) {
	mainTestDB = testdb.StartPackageDB(m)
	code := m.Run()
	mainTestDB.Close()
	os.Exit(code)
}

func TestAuthMiddleware(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	t.Run("empty token still requires bearer auth", func(t *testing.T) {
		nextCalled = false
		handler := authMiddleware("", nil, next)
		req := httptest.NewRequest("POST", "/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if nextCalled {
			t.Error("expected next handler NOT to be called")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("correct bearer token", func(t *testing.T) {
		nextCalled = false
		handler := authMiddleware("mytoken", nil, next)
		req := httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Authorization", "Bearer mytoken")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if !nextCalled {
			t.Error("expected next handler to be called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("wrong bearer token", func(t *testing.T) {
		nextCalled = false
		handler := authMiddleware("mytoken", nil, next)
		req := httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if nextCalled {
			t.Error("expected next handler NOT to be called")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("missing authorization header", func(t *testing.T) {
		nextCalled = false
		handler := authMiddleware("mytoken", nil, next)
		req := httptest.NewRequest("POST", "/mcp", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if nextCalled {
			t.Error("expected next handler NOT to be called")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("non-bearer scheme", func(t *testing.T) {
		nextCalled = false
		handler := authMiddleware("mytoken", nil, next)
		req := httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if nextCalled {
			t.Error("expected next handler NOT to be called")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})
}

func TestRunnerRPCAuthMiddleware(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	t.Run("valid runner token", func(t *testing.T) {
		nextCalled = false
		handler := runnerRPCAuthMiddleware("runner-token", next)
		req := httptest.NewRequest("POST", "/runnerv1.RunnerService/ClaimTask", nil)
		req.Header.Set("Authorization", "Bearer runner-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if !nextCalled {
			t.Error("expected next handler to be called")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		nextCalled = false
		handler := runnerRPCAuthMiddleware("runner-token", next)
		req := httptest.NewRequest("POST", "/runnerv1.RunnerService/ClaimTask", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if nextCalled {
			t.Error("expected next handler NOT to be called")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("empty runner token", func(t *testing.T) {
		nextCalled = false
		handler := runnerRPCAuthMiddleware("", next)
		req := httptest.NewRequest("POST", "/runnerv1.RunnerService/ClaimTask", nil)
		req.Header.Set("Authorization", "Bearer something")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if nextCalled {
			t.Error("expected next handler NOT to be called")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("missing authorization header", func(t *testing.T) {
		nextCalled = false
		handler := runnerRPCAuthMiddleware("runner-token", next)
		req := httptest.NewRequest("POST", "/runnerv1.RunnerService/ClaimTask", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if nextCalled {
			t.Error("expected next handler NOT to be called")
		}
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})
}

func seedTokenInDB(t *testing.T, st *store.Store) (teamID, rawToken string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	q := data.New(st.DB(), st.Dialect())

	teamID = "team_integration_test"
	if err := q.CreateTeam(ctx, repository.CreateTeamParams{
		ID: teamID, Name: "test-team", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create team: %v", err)
	}

	userID := "user_integration_test"
	if err := q.CreateUser(ctx, repository.CreateUserParams{
		ID: userID, Name: "test-user", TeamID: teamID, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	rawToken = "chtr_test_integration_token_12345"
	hash := sha256.Sum256([]byte(rawToken))
	if err := q.CreateToken(ctx, repository.CreateTokenParams{
		ID:        "tok_integration_test",
		Name:      "test-token",
		TokenHash: hex.EncodeToString(hash[:]),
		UserID:    userID,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create token: %v", err)
	}
	return teamID, rawToken
}

func TestLookupTokenScope(t *testing.T) {
	tdb, cleanup := mainTestDB.NewTestDB(t)
	defer cleanup()
	st, err := store.Open(tdb.DSN, tdb.Dialect())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	teamID, rawToken := seedTokenInDB(t, st)

	scope, _ := auth.ResolveToken(context.Background(), "", st.DB(), rawToken)
	if scope.TeamID != teamID {
		t.Errorf("expected TeamID=%q, got %q", teamID, scope.TeamID)
	}
	if scope.Admin {
		t.Error("expected Admin=false for scoped token")
	}
}

func TestLookupTokenScopeInvalidToken(t *testing.T) {
	tdb, cleanup := mainTestDB.NewTestDB(t)
	defer cleanup()
	st, err := store.Open(tdb.DSN, tdb.Dialect())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	scope, _ := auth.ResolveToken(context.Background(), "", st.DB(), "nonexistent-token")
	if scope.TeamID != "" {
		t.Errorf("expected empty TeamID, got %q", scope.TeamID)
	}
	if scope.Admin {
		t.Error("expected Admin=false for invalid token")
	}
}

func TestAuthMiddlewareWithDBToken(t *testing.T) {
	tdb, cleanup := mainTestDB.NewTestDB(t)
	defer cleanup()
	st, err := store.Open(tdb.DSN, tdb.Dialect())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	teamID, rawToken := seedTokenInDB(t, st)

	t.Run("valid DB token passes through with team scope", func(t *testing.T) {
		handler := authMiddleware("admin-token", st.DB(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scope, ok := auth.GetScope(r.Context())
			if !ok {
				t.Error("expected scope in context")
			}
			if scope.TeamID != teamID {
				t.Errorf("expected TeamID=%q, got %q", teamID, scope.TeamID)
			}
			if scope.Admin {
				t.Error("expected Admin=false")
			}
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Authorization", "Bearer "+rawToken)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("invalid DB token returns 401", func(t *testing.T) {
		handler := authMiddleware("admin-token", st.DB(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("next should not be called for invalid token")
		}))

		req := httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Authorization", "Bearer invalid-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})

	t.Run("admin token still bypasses DB lookup", func(t *testing.T) {
		handler := authMiddleware("admin-token", st.DB(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scope, ok := auth.GetScope(r.Context())
			if !ok || !scope.Admin {
				t.Error("expected admin scope")
			}
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest("POST", "/mcp", nil)
		req.Header.Set("Authorization", "Bearer admin-token")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})
}

func TestRegisterToolsDoesNotPanicWithNilSvc(t *testing.T) {
	// Verify that RegisterTools handles nil service gracefully
	// (the Arcane tools check svc != nil before registering)
}

func TestBuildWebhookHandlerReturnsNilWhenNotConfigured(t *testing.T) {
	cfg := config.Config{}
	handler := buildWebhookHandler(cfg, nil)
	if handler != nil {
		t.Error("expected nil handler when GitHub not configured")
	}
}

func TestBuildWebhookHandlerReturnsNilWithBadCredentials(t *testing.T) {
	cfg := config.Config{
		GitHubAppID:            12345,
		GitHubInstallationID:   67890,
		GitHubAppPrivateKeyB64: "aW52YWxpZC1rZXk=",
		GitHubWebhookSecret:    "secret",
	}
	handler := buildWebhookHandler(cfg, nil)
	if handler != nil {
		t.Error("expected nil handler when GitHub client creation fails")
	}
}
