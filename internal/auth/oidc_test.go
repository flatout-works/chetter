package auth

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/oidctest"
	"github.com/golang-jwt/jwt/v5"
)

const testSessionSecret = "test-session-secret-at-least-32-bytes"

// newTestOIDC builds an OIDCAuth backed by a local fake provider. The runner
// sandbox blocks outbound HTTP, so all tests must use the httptest provider
// rather than a public issuer URL.
func newTestOIDC(t *testing.T, mutate func(*OIDCConfig)) *OIDCAuth {
	t.Helper()
	provider := oidctest.New(t, "test-client")
	cfg := OIDCConfig{
		IssuerURL:       provider.Issuer,
		ClientID:        "test-client",
		ClientSecret:    "test-secret",
		RedirectURL:     "http://localhost:8090/auth/callback",
		AdminGroup:      DefaultAdminGroup,
		TeamGroupPrefix: DefaultTeamGroupPrefix,
		SessionSecret:   testSessionSecret,
		SessionTTL:      time.Hour,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	a, err := NewOIDCAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOIDCAuth: %v", err)
	}
	return a
}

func TestScopeForGroups(t *testing.T) {
	tests := []struct {
		name   string
		groups []string
		prefix string
		admin  string
		want   Scope
	}{
		{
			name:   "admin group only",
			groups: []string{"chetter-admin", "engineering"},
			want:   Scope{Admin: true},
		},
		{
			name:   "team groups",
			groups: []string{"chetter-platform", "chetter-data", "random-group"},
			want:   Scope{TeamID: "platform", TeamIDs: []string{"platform", "data"}},
		},
		{
			name:   "admin plus teams",
			groups: []string{"chetter-admin", "chetter-platform"},
			want:   Scope{Admin: true, TeamID: "platform", TeamIDs: []string{"platform"}},
		},
		{
			name:   "unrelated groups ignored",
			groups: []string{"everyone", "developers"},
			want:   Scope{},
		},
		{
			name:   "custom admin group and prefix",
			groups: []string{"ops-admins", "ops-web"},
			prefix: "ops-",
			admin:  "ops-admins",
			want:   Scope{Admin: true, TeamID: "web", TeamIDs: []string{"web"}},
		},
		{
			name:   "prefix without team name ignored",
			groups: []string{"chetter-"},
			want:   Scope{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestOIDC(t, func(cfg *OIDCConfig) {
				if tt.prefix != "" {
					cfg.TeamGroupPrefix = tt.prefix
				}
				if tt.admin != "" {
					cfg.AdminGroup = tt.admin
				}
			})
			got := a.ScopeForGroups(tt.groups)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ScopeForGroups(%v) = %+v, want %+v", tt.groups, got, tt.want)
			}
		})
	}
}

func TestNewOIDCAuthValidation(t *testing.T) {
	provider := oidctest.New(t, "test-client")

	cfg := OIDCConfig{
		IssuerURL:     provider.Issuer,
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		RedirectURL:   "http://localhost:8090/auth/callback",
		SessionSecret: "",
	}
	// Missing session secret is rejected before provider discovery.
	if _, err := NewOIDCAuth(context.Background(), cfg); err == nil {
		t.Fatal("expected error for missing session secret")
	}

	cfg.SessionSecret = "too-short"
	if _, err := NewOIDCAuth(context.Background(), cfg); err == nil {
		t.Fatal("expected error for short session secret")
	}

	cfg.SessionSecret = testSessionSecret
	cfg.IssuerURL = ""
	if _, err := NewOIDCAuth(context.Background(), cfg); err == nil {
		t.Fatal("expected error for missing issuer URL")
	}
}

func TestLoginURLIncludesStateAndNonce(t *testing.T) {
	provider := oidctest.New(t, "test-client")
	cfg := OIDCConfig{
		IssuerURL:     provider.Issuer,
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		RedirectURL:   "http://localhost:8090/auth/callback",
		SessionSecret: testSessionSecret,
		SessionTTL:    time.Hour,
	}
	a, err := NewOIDCAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOIDCAuth: %v", err)
	}
	loginURL := a.LoginURL("state-123", "nonce-456")
	if got := oidctest.Query(loginURL, "state"); got != "state-123" {
		t.Errorf("state = %q, want state-123", got)
	}
	if got := oidctest.Query(loginURL, "nonce"); got != "nonce-456" {
		t.Errorf("nonce = %q, want nonce-456", got)
	}
	if got := oidctest.Query(loginURL, "client_id"); got != "test-client" {
		t.Errorf("client_id = %q, want test-client", got)
	}
	if got := oidctest.Query(loginURL, "redirect_uri"); got != cfg.RedirectURL {
		t.Errorf("redirect_uri = %q, want %q", got, cfg.RedirectURL)
	}
	if got := oidctest.Query(loginURL, "scope"); got != "openid profile email groups" {
		t.Errorf("scope = %q", got)
	}
}

func TestExchangeVerifiesIDToken(t *testing.T) {
	provider := oidctest.New(t, "test-client")
	cfg := OIDCConfig{
		IssuerURL:     provider.Issuer,
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		RedirectURL:   "http://localhost:8090/auth/callback",
		SessionSecret: testSessionSecret,
		SessionTTL:    time.Hour,
	}
	a, err := NewOIDCAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOIDCAuth: %v", err)
	}

	verified := true
	code := provider.IssueCode(oidctest.TokenSpec{
		Subject:       "user-1",
		Email:         "alice@example.com",
		EmailVerified: &verified,
		Groups:        []string{"chetter-admin", "chetter-platform"},
		Nonce:         "nonce-456",
	})
	identity, err := a.Exchange(context.Background(), code, "nonce-456")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if identity.Subject != "user-1" || identity.Email != "alice@example.com" {
		t.Errorf("identity = %+v", identity)
	}
	if !identity.EmailVerified {
		t.Error("EmailVerified = false, want true")
	}
	if !reflect.DeepEqual(identity.Groups, []string{"chetter-admin", "chetter-platform"}) {
		t.Errorf("Groups = %v", identity.Groups)
	}
}

func TestExchangeRejectsWrongNonce(t *testing.T) {
	provider := oidctest.New(t, "test-client")
	cfg := OIDCConfig{
		IssuerURL:     provider.Issuer,
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		RedirectURL:   "http://localhost:8090/auth/callback",
		SessionSecret: testSessionSecret,
		SessionTTL:    time.Hour,
	}
	a, err := NewOIDCAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOIDCAuth: %v", err)
	}
	code := provider.IssueCode(oidctest.TokenSpec{
		Subject: "user-1",
		Email:   "alice@example.com",
		Nonce:   "nonce-456",
	})
	if _, err := a.Exchange(context.Background(), code, "wrong-nonce"); err == nil {
		t.Fatal("expected nonce mismatch error")
	}
}

func TestExchangeRejectsUnknownCode(t *testing.T) {
	a := newTestOIDC(t, nil)
	if _, err := a.Exchange(context.Background(), "bogus-code", ""); err == nil {
		t.Fatal("expected exchange error for unknown code")
	}
}

func TestSessionRoundTrip(t *testing.T) {
	a := newTestOIDC(t, nil)
	scope := Scope{Admin: true, TeamID: "platform", TeamIDs: []string{"platform"}}
	session, err := a.NewSession(&OIDCIdentity{Subject: "user-1", Email: "a@example.com"}, scope)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	claims, err := a.ParseSession(session)
	if err != nil {
		t.Fatalf("ParseSession: %v", err)
	}
	if claims.Subject != "user-1" || claims.Email != "a@example.com" {
		t.Errorf("claims = %+v", claims)
	}
	if !claims.Admin || !reflect.DeepEqual(claims.TeamIDs, []string{"platform"}) {
		t.Errorf("claims scope = admin=%v teams=%v", claims.Admin, claims.TeamIDs)
	}
	got := claims.Scope()
	if !got.Admin || !reflect.DeepEqual(got.TeamIDs, []string{"platform"}) {
		t.Errorf("Scope() = %+v", got)
	}
	var rawClaims jwt.MapClaims
	if _, _, err := new(jwt.Parser).ParseUnverified(session, &rawClaims); err != nil {
		t.Fatalf("ParseUnverified: %v", err)
	}
	if _, ok := rawClaims["id_token"]; ok {
		t.Fatal("session contains upstream id_token")
	}
}

func TestSessionRequiresHS256IssuerAndExpiry(t *testing.T) {
	a := newTestOIDC(t, nil)
	now := time.Now().UTC()
	tests := []struct {
		name   string
		method jwt.SigningMethod
		claims SessionClaims
	}{
		{
			name:   "wrong algorithm",
			method: jwt.SigningMethodHS384,
			claims: SessionClaims{RegisteredClaims: jwt.RegisteredClaims{Issuer: "chetter-web", ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}},
		},
		{
			name:   "wrong issuer",
			method: jwt.SigningMethodHS256,
			claims: SessionClaims{RegisteredClaims: jwt.RegisteredClaims{Issuer: "other", ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}},
		},
		{
			name:   "missing expiry",
			method: jwt.SigningMethodHS256,
			claims: SessionClaims{RegisteredClaims: jwt.RegisteredClaims{Issuer: "chetter-web"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signed, err := jwt.NewWithClaims(tt.method, tt.claims).SignedString([]byte(testSessionSecret))
			if err != nil {
				t.Fatalf("sign token: %v", err)
			}
			if _, err := a.ParseSession(signed); err == nil {
				t.Fatal("ParseSession accepted invalid token")
			}
		})
	}
}

func TestSessionRejectsTamperedToken(t *testing.T) {
	a := newTestOIDC(t, nil)
	session, err := a.NewSession(&OIDCIdentity{Subject: "user-1"}, Scope{Admin: true})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// Flip a character in the payload section.
	tampered := session[:len(session)/2] + "x" + session[len(session)/2+1:]
	if _, err := a.ParseSession(tampered); err == nil {
		t.Fatal("expected error for tampered session token")
	}
}

func TestSessionRejectsWrongSecret(t *testing.T) {
	a := newTestOIDC(t, nil)
	other := newTestOIDC(t, func(cfg *OIDCConfig) {
		cfg.SessionSecret = "different-session-secret-at-least-32-bytes"
	})
	session, err := a.NewSession(&OIDCIdentity{Subject: "user-1"}, Scope{})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if _, err := other.ParseSession(session); err == nil {
		t.Fatal("expected error parsing session signed with a different secret")
	}
}

func TestSessionExpiry(t *testing.T) {
	a := newTestOIDC(t, nil)
	expired := jwt.NewWithClaims(jwt.SigningMethodHS256, SessionClaims{
		Subject: "user-1",
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   "user-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Minute)),
		},
	})
	session, err := expired.SignedString([]byte(testSessionSecret))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}
	if _, err := a.ParseSession(session); err == nil {
		t.Fatal("expected error for expired session token")
	}
}

func TestSessionCookie(t *testing.T) {
	a := newTestOIDC(t, nil)
	session, err := a.NewSession(&OIDCIdentity{Subject: "user-1"}, Scope{TeamID: "t1", TeamIDs: []string{"t1"}})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	headers := map[string][]string{"Cookie": {SessionCookieName + "=" + session}}
	scope, ok := a.ScopeFromCookie(headers)
	if !ok {
		t.Fatal("ScopeFromCookie returned ok=false for valid session")
	}
	if !scope.HasTeam("t1") {
		t.Errorf("scope = %+v, want team t1", scope)
	}
	secureHeaders := map[string][]string{"Cookie": {SessionCookieSecureName + "=" + session}}
	if secureScope, ok := a.ScopeFromCookie(secureHeaders); !ok || !secureScope.HasTeam("t1") {
		t.Errorf("secure cookie scope = %+v, ok=%v", secureScope, ok)
	}

	// Missing cookie.
	if _, ok := a.ScopeFromCookie(map[string][]string{}); ok {
		t.Fatal("ScopeFromCookie returned ok=true without cookie")
	}
	// Garbage cookie.
	garbage := map[string][]string{"Cookie": {SessionCookieName + "=garbage"}}
	if _, ok := a.ScopeFromCookie(garbage); ok {
		t.Fatal("ScopeFromCookie returned ok=true for garbage cookie")
	}
}

func TestEndSessionEndpoint(t *testing.T) {
	provider := oidctest.New(t, "test-client")
	cfg := OIDCConfig{
		IssuerURL:     provider.Issuer,
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		RedirectURL:   "http://localhost:8090/auth/callback",
		SessionSecret: testSessionSecret,
		SessionTTL:    time.Hour,
	}
	a, err := NewOIDCAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOIDCAuth: %v", err)
	}
	if got := a.EndSessionEndpoint(); got != provider.Issuer+"/logout" {
		t.Errorf("EndSessionEndpoint = %q", got)
	}
}

func TestRedirectOrigin(t *testing.T) {
	provider := oidctest.New(t, "test-client")
	cfg := OIDCConfig{
		IssuerURL:     provider.Issuer,
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		RedirectURL:   "https://chetter.example.com/auth/callback",
		SessionSecret: testSessionSecret,
		SessionTTL:    time.Hour,
	}
	a, err := NewOIDCAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOIDCAuth: %v", err)
	}
	tests := []struct {
		name     string
		auth     *OIDCAuth
		redirect string
		want     string
	}{
		{name: "plain origin", auth: a, want: "https://chetter.example.com"},
		{name: "with port", auth: a, redirect: "http://localhost:8090/auth/callback", want: "http://localhost:8090"},
		{name: "nil auth", auth: nil, want: ""},
		{name: "unparseable", auth: a, redirect: "not a url", want: ""},
		{name: "no host", auth: a, redirect: "http://", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.auth != nil && tt.redirect != "" {
				tt.auth.cfg.RedirectURL = tt.redirect
			}
			if got := tt.auth.RedirectOrigin(); got != tt.want {
				t.Errorf("RedirectOrigin() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSecureCookiesUsesConfiguredRedirectURL(t *testing.T) {
	a := newTestOIDC(t, nil)
	if a.SecureCookies() {
		t.Fatal("SecureCookies() = true for HTTP redirect URL")
	}
	a.cfg.RedirectURL = "https://chetter.example.com/auth/callback"
	if !a.SecureCookies() {
		t.Fatal("SecureCookies() = false for HTTPS redirect URL")
	}
	a.cfg.RedirectURL = "https://"
	if a.SecureCookies() {
		t.Fatal("SecureCookies() = true for redirect URL without host")
	}
}

func TestTeamGroupPrefix(t *testing.T) {
	provider := oidctest.New(t, "test-client")
	cfg := OIDCConfig{
		IssuerURL:     provider.Issuer,
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		RedirectURL:   "http://localhost:8090/auth/callback",
		SessionSecret: testSessionSecret,
		SessionTTL:    time.Hour,
	}
	a, err := NewOIDCAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOIDCAuth: %v", err)
	}
	if got := a.TeamGroupPrefix(); got != DefaultTeamGroupPrefix {
		t.Errorf("default TeamGroupPrefix = %q, want %q", got, DefaultTeamGroupPrefix)
	}
	a.cfg.TeamGroupPrefix = "team-"
	if got := a.TeamGroupPrefix(); got != "team-" {
		t.Errorf("custom TeamGroupPrefix = %q, want %q", got, "team-")
	}
	a.cfg.TeamGroupPrefix = ""
	if got := a.TeamGroupPrefix(); got != DefaultTeamGroupPrefix {
		t.Errorf("empty TeamGroupPrefix = %q, want %q", got, DefaultTeamGroupPrefix)
	}
	var nilAuth *OIDCAuth
	if got := nilAuth.TeamGroupPrefix(); got != DefaultTeamGroupPrefix {
		t.Errorf("nil TeamGroupPrefix = %q, want %q", got, DefaultTeamGroupPrefix)
	}
}

func TestStringGroups(t *testing.T) {
	tests := []struct {
		name string
		raw  any
		want []string
	}{
		{name: "string slice", raw: []string{"a", "b"}, want: []string{"a", "b"}},
		{name: "interface slice", raw: []any{"a", "b"}, want: []string{"a", "b"}},
		{name: "interface slice with non-strings", raw: []any{"a", 42, ""}, want: []string{"a"}},
		{name: "single string", raw: "a", want: []string{"a"}},
		{name: "empty string", raw: "", want: nil},
		{name: "nil", raw: nil, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stringGroups(tt.raw); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("stringGroups(%v) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
