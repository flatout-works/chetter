package webapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/oidctest"
)

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header http.Header
		want   string
	}{
		{
			name:   "valid bearer token",
			header: http.Header{"Authorization": {"Bearer mytoken123"}},
			want:   "mytoken123",
		},
		{
			name:   "missing authorization header",
			header: http.Header{},
			want:   "",
		},
		{
			name:   "empty authorization",
			header: http.Header{"Authorization": {""}},
			want:   "",
		},
		{
			name:   "not bearer (basic auth)",
			header: http.Header{"Authorization": {"Basic dXNlcjpwYXNz"}},
			want:   "",
		},
		{
			name:   "bearer with extra spaces",
			header: http.Header{"Authorization": {"Bearer   token-with-spaces"}},
			want:   "  token-with-spaces",
		},
		{
			name:   "lowercase bearer",
			header: http.Header{"Authorization": {"bearer token123"}},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := bearerToken(tt.header)
			if got != tt.want {
				t.Errorf("bearerToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthInterceptorResolveWithSessionCookie(t *testing.T) {
	provider := oidctest.New(t, "test-client")
	oidc, err := auth.NewOIDCAuth(context.Background(), auth.OIDCConfig{
		IssuerURL:     provider.Issuer,
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		RedirectURL:   "http://localhost:8090/auth/callback",
		SessionSecret: "test-session-secret-at-least-32-bytes",
		SessionTTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("NewOIDCAuth: %v", err)
	}
	interceptor := NewAuthInterceptor("admin-token", nil, oidc)
	a := interceptor.(*authInterceptor)

	// No credentials at all -> rejected.
	if _, ok := a.resolve(context.Background(), http.Header{}); ok {
		t.Fatal("resolve accepted request without credentials")
	}

	// Bearer admin token still works.
	header := http.Header{"Authorization": {"Bearer admin-token"}}
	scope, ok := a.resolve(context.Background(), header)
	if !ok || !scope.Admin {
		t.Fatalf("resolve with admin bearer: ok=%v scope=%+v", ok, scope)
	}

	// Session cookie grants the session scope.
	session, err := oidc.NewSession(&auth.OIDCIdentity{Subject: "user-1", Email: "a@example.com"}, auth.Scope{TeamIDs: []string{"t1"}})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	cookieHeader := http.Header{"Cookie": {auth.SessionCookieName + "=" + session}}
	scope, ok = a.resolve(context.Background(), cookieHeader)
	if !ok || !scope.HasTeam("t1") || scope.Admin {
		t.Fatalf("resolve with session cookie: ok=%v scope=%+v", ok, scope)
	}

	// Invalid cookie -> rejected.
	bad := http.Header{"Cookie": {auth.SessionCookieName + "=not-a-jwt"}}
	if _, ok := a.resolve(context.Background(), bad); ok {
		t.Fatal("resolve accepted invalid session cookie")
	}
}

func TestAuthInterceptorResolveWithoutOIDC(t *testing.T) {
	interceptor := NewAuthInterceptor("admin-token", nil, nil)
	a := interceptor.(*authInterceptor)

	if _, ok := a.resolve(context.Background(), http.Header{"Cookie": {auth.SessionCookieName + "=whatever"}}); ok {
		t.Fatal("resolve accepted session cookie when OIDC is disabled")
	}
	if _, ok := a.resolve(context.Background(), http.Header{}); ok {
		t.Fatal("resolve accepted empty request when OIDC is disabled")
	}
}
