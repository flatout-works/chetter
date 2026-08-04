package webapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/oidctest"
	"github.com/flatout-works/chetter/internal/repository"
)

const testSessionSecret = "test-session-secret"

// newTestOIDC builds an OIDCAuth backed by the local fake provider and
// returns both so tests can issue codes.
func newTestOIDC(t *testing.T) (*auth.OIDCAuth, *oidctest.FakeProvider) {
	t.Helper()
	provider := oidctest.New(t, "test-client")
	a, err := auth.NewOIDCAuth(context.Background(), auth.OIDCConfig{
		IssuerURL:       provider.Issuer,
		ClientID:        "test-client",
		ClientSecret:    "test-secret",
		RedirectURL:     "http://localhost:8090/auth/callback",
		AdminGroup:      auth.DefaultAdminGroup,
		TeamGroupPrefix: auth.DefaultTeamGroupPrefix,
		SessionSecret:   testSessionSecret,
		SessionTTL:      time.Hour,
	})
	if err != nil {
		t.Fatalf("NewOIDCAuth: %v", err)
	}
	return a, provider
}

func newNoRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func newOIDCTestServer(t *testing.T, oidc *auth.OIDCAuth) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	RegisterOIDCRoutes(mux, oidc, nil)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestOIDCLoginRedirect(t *testing.T) {
	a, _ := newTestOIDC(t)
	server := newOIDCTestServer(t, a)

	resp, err := newNoRedirectClient().Get(server.URL + "/auth/login")
	if err != nil {
		t.Fatalf("GET /auth/login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	location := resp.Header.Get("Location")
	if location == "" {
		t.Fatal("missing Location header")
	}
	if oidctest.Query(location, "client_id") != "test-client" {
		t.Errorf("login URL missing client_id: %s", location)
	}

	cookies := resp.Cookies()
	var stateCookie, nonceCookie *http.Cookie
	for _, c := range cookies {
		switch c.Name {
		case auth.OAuthStateCookieName:
			stateCookie = c
		case auth.OAuthNonceCookieName:
			nonceCookie = c
		}
	}
	if stateCookie == nil || nonceCookie == nil {
		t.Fatalf("expected state and nonce cookies, got %v", cookies)
	}
	if !stateCookie.HttpOnly || stateCookie.MaxAge != auth.OAuthStateCookieMaxAge {
		t.Errorf("state cookie attributes: %+v", stateCookie)
	}
	// The state value must match the one in the redirect URL.
	if oidctest.Query(location, "state") != stateCookie.Value {
		t.Errorf("state mismatch: cookie %q, URL %q", stateCookie.Value, oidctest.Query(location, "state"))
	}
	if oidctest.Query(location, "nonce") != nonceCookie.Value {
		t.Errorf("nonce mismatch: cookie %q, URL %q", nonceCookie.Value, oidctest.Query(location, "nonce"))
	}
}

func TestOIDCCallbackFullFlow(t *testing.T) {
	a, provider := newTestOIDC(t)
	server := newOIDCTestServer(t, a)

	// 1. Start the login flow and capture the state/nonce cookies.
	loginResp, err := newNoRedirectClient().Get(server.URL + "/auth/login")
	if err != nil {
		t.Fatalf("GET /auth/login: %v", err)
	}
	loginResp.Body.Close()
	jar := newCookieJar(loginResp.Cookies())

	// 2. Simulate the IdP redirecting back with a code.
	verified := true
	code := provider.IssueCode(oidctest.TokenSpec{
		Subject:       "user-1",
		Email:         "alice@example.com",
		EmailVerified: &verified,
		Groups:        []string{"chetter-admin", "chetter-platform"},
		Nonce:         cookieValue(t, jar, auth.OAuthNonceCookieName),
	})

	callbackReq, err := http.NewRequest(http.MethodGet, server.URL+"/auth/callback?code="+code+"&state="+cookieValue(t, jar, auth.OAuthStateCookieName), nil)
	if err != nil {
		t.Fatalf("build callback request: %v", err)
	}
	for _, c := range jar {
		callbackReq.AddCookie(c)
	}
	callbackResp, err := newNoRedirectClient().Do(callbackReq)
	if err != nil {
		t.Fatalf("GET /auth/callback: %v", err)
	}
	callbackResp.Body.Close()
	if callbackResp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", callbackResp.StatusCode)
	}
	if loc := callbackResp.Header.Get("Location"); loc != "/" {
		t.Errorf("callback redirect = %q, want /", loc)
	}

	// Session cookie must be set with the expected attributes.
	var sessionCookie *http.Cookie
	for _, c := range callbackResp.Cookies() {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("callback did not set session cookie")
	}
	if !sessionCookie.HttpOnly || sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("session cookie attributes: %+v", sessionCookie)
	}
	if sessionCookie.MaxAge != int(time.Hour.Seconds()) {
		t.Errorf("session MaxAge = %d, want %d", sessionCookie.MaxAge, int(time.Hour.Seconds()))
	}

	// 3. The session cookie must now authenticate the /auth/session endpoint.
	sessionReq, _ := http.NewRequest(http.MethodGet, server.URL+"/auth/session", nil)
	sessionReq.AddCookie(sessionCookie)
	sessionResp, err := newNoRedirectClient().Do(sessionReq)
	if err != nil {
		t.Fatalf("GET /auth/session: %v", err)
	}
	defer sessionResp.Body.Close()
	if sessionResp.StatusCode != http.StatusOK {
		t.Fatalf("session status = %d, want 200", sessionResp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(sessionResp.Body).Decode(&body); err != nil {
		t.Fatalf("decode session body: %v", err)
	}
	if body["authenticated"] != true || body["email"] != "alice@example.com" || body["admin"] != true {
		t.Errorf("session body = %v", body)
	}

	// 4. And the scope must grant access to the resolved team.
	claims, ok := a.SessionFromCookie(sessionResp.Request.Header)
	if !ok {
		t.Fatal("session not valid")
	}
	if !claims.Scope().Admin || !claims.Scope().HasTeam("platform") {
		t.Errorf("session scope = %+v", claims.Scope())
	}
}

func TestOIDCCallbackRejectsStateMismatch(t *testing.T) {
	a, provider := newTestOIDC(t)
	server := newOIDCTestServer(t, a)

	code := provider.IssueCode(oidctest.TokenSpec{Subject: "user-1", Email: "a@example.com"})
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/auth/callback?code="+code+"&state=wrong-state", nil)
	resp, err := newNoRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("GET /auth/callback: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName {
			t.Fatal("session cookie set despite state mismatch")
		}
	}
}

func TestOIDCCallbackRejectsProviderError(t *testing.T) {
	a, _ := newTestOIDC(t)
	server := newOIDCTestServer(t, a)

	resp, err := http.Get(server.URL + "/auth/callback?error=access_denied&error_description=user+cancelled")
	if err != nil {
		t.Fatalf("GET /auth/callback: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestOIDCSessionEndpoint(t *testing.T) {
	a, _ := newTestOIDC(t)
	server := newOIDCTestServer(t, a)

	// No cookie -> 401.
	resp, err := newNoRedirectClient().Get(server.URL + "/auth/session")
	if err != nil {
		t.Fatalf("GET /auth/session: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}

	// Valid session cookie -> 200 with claims.
	session, err := a.NewSession(&auth.OIDCIdentity{Subject: "user-1", Email: "bob@example.com"}, auth.Scope{TeamIDs: []string{"t1"}}, "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/auth/session", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	resp, err = newNoRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("GET /auth/session: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["authenticated"] != true || body["email"] != "bob@example.com" {
		t.Errorf("session body = %v", body)
	}
}

func TestOIDCLogout(t *testing.T) {
	a, _ := newTestOIDC(t)
	server := newOIDCTestServer(t, a)

	session, err := a.NewSession(&auth.OIDCIdentity{Subject: "user-1"}, auth.Scope{}, "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	resp, err := newNoRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("GET /auth/logout: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}

	// The fake provider advertises an end-session endpoint; the redirect
	// must go there with a post-logout redirect back to the app. The URI is
	// derived from the configured OIDC_REDIRECT_URL origin, not request
	// headers (the httptest server URL differs from the config).
	location := resp.Header.Get("Location")
	if got := oidctest.Query(location, "post_logout_redirect_uri"); got != "http://localhost:8090/" {
		t.Errorf("post_logout_redirect_uri = %q, want %q", got, "http://localhost:8090/")
	}

	// The session cookie must be cleared.
	var cleared bool
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName && c.MaxAge == -1 {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("session cookie not cleared: %v", resp.Cookies())
	}
}

func TestOIDCLogoutWithoutEndSessionEndpoint(t *testing.T) {
	// A provider without an end-session endpoint still clears the cookie and
	// redirects to the app root.
	provider := oidctest.New(t, "test-client")
	provider.NoEndSession = true
	a, err := auth.NewOIDCAuth(context.Background(), auth.OIDCConfig{
		IssuerURL:     provider.Issuer,
		ClientID:      "test-client",
		ClientSecret:  "test-secret",
		RedirectURL:   "http://localhost:8090/auth/callback",
		SessionSecret: testSessionSecret,
		SessionTTL:    time.Hour,
	})
	if err != nil {
		t.Fatalf("NewOIDCAuth: %v", err)
	}
	mux := http.NewServeMux()
	RegisterOIDCRoutes(mux, a, nil)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	session, err := a.NewSession(&auth.OIDCIdentity{Subject: "user-1"}, auth.Scope{}, "")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/auth/logout", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: session})
	resp, err := newNoRedirectClient().Do(req)
	if err != nil {
		t.Fatalf("GET /auth/logout: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
}

type cookieJar []*http.Cookie

func newCookieJar(cookies []*http.Cookie) cookieJar {
	jar := make(cookieJar, 0, len(cookies))
	jar = append(jar, cookies...)
	return jar
}

func cookieValue(t *testing.T, jar cookieJar, name string) string {
	t.Helper()
	for _, c := range jar {
		if c.Name == name {
			return c.Value
		}
	}
	t.Fatalf("cookie %q not found in jar %v", name, jar)
	return ""
}

// fakeTeamResolver is a TeamResolver for tests.
type fakeTeamResolver struct {
	teams []repository.Team
	err   error
}

func (f fakeTeamResolver) ListTeams(context.Context) ([]repository.Team, error) {
	return f.teams, f.err
}

func teamWithOkta(id, groupID, groupName string) repository.Team {
	t := repository.Team{ID: id, Name: "platform"}
	if groupID != "" {
		t.OktaGroupID = sql.NullString{String: groupID, Valid: true}
	}
	if groupName != "" {
		t.OktaGroupName = sql.NullString{String: groupName, Valid: true}
	}
	return t
}

func TestResolveTeamIDs(t *testing.T) {
	a, _ := newTestOIDC(t)
	tests := []struct {
		name     string
		resolver TeamResolver
		groups   []string
		names    []string
		want     []string
	}{
		{
			name:   "nil resolver keeps derived names",
			groups: []string{"chetter-platform"},
			names:  []string{"platform"},
			want:   []string{"platform"},
		},
		{
			name:     "name lookup resolves to team id",
			resolver: fakeTeamResolver{teams: []repository.Team{{ID: "t1", Name: "platform"}}},
			groups:   []string{"chetter-platform"},
			names:    []string{"platform"},
			want:     []string{"t1"},
		},
		{
			name:     "unknown team kept literal",
			resolver: fakeTeamResolver{},
			groups:   []string{"chetter-platform"},
			names:    []string{"platform"},
			want:     []string{"platform"},
		},
		{
			name: "okta group name binding wins over name lookup",
			resolver: fakeTeamResolver{teams: []repository.Team{
				{ID: "t-platform", Name: "platform"},
				teamWithOkta("t-eng", "", "chetter-platform"),
			}},
			groups: []string{"chetter-platform"},
			names:  []string{"platform"},
			want:   []string{"t-eng"},
		},
		{
			name:     "okta group id binding maps without prefix",
			resolver: fakeTeamResolver{teams: []repository.Team{teamWithOkta("t-eng", "00g123", "")}},
			groups:   []string{"00g123"},
			names:    []string{},
			want:     []string{"t-eng"},
		},
		{
			name:     "non-prefixed group bound via okta group name",
			resolver: fakeTeamResolver{teams: []repository.Team{teamWithOkta("t-pay", "", "payments")}},
			groups:   []string{"payments"},
			names:    []string{},
			want:     []string{"t-pay"},
		},
		{
			name:     "duplicate groups and names deduplicated",
			resolver: fakeTeamResolver{teams: []repository.Team{{ID: "t1", Name: "platform"}}},
			groups:   []string{"chetter-platform", "chetter-platform"},
			names:    []string{"platform", "platform"},
			want:     []string{"t1"},
		},
		{
			name:     "list error falls back to derived names",
			resolver: fakeTeamResolver{err: errors.New("boom")},
			groups:   []string{"chetter-platform"},
			names:    []string{"platform"},
			want:     []string{"platform"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &oidcHandlers{oidc: a, teams: tt.resolver}
			got := h.resolveTeamIDs(context.Background(), tt.groups, tt.names)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resolveTeamIDs(%v, %v) = %v, want %v", tt.groups, tt.names, got, tt.want)
			}
		})
	}
}

func TestOIDCCallbackResolvesTeamsViaFacade(t *testing.T) {
	a, provider := newTestOIDC(t)
	resolver := fakeTeamResolver{teams: []repository.Team{{ID: "team-platform-id", Name: "platform"}}}
	mux := http.NewServeMux()
	RegisterOIDCRoutes(mux, a, resolver)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	// Login to capture the state/nonce cookies.
	loginResp, err := newNoRedirectClient().Get(server.URL + "/auth/login")
	if err != nil {
		t.Fatalf("GET /auth/login: %v", err)
	}
	loginResp.Body.Close()
	jar := newCookieJar(loginResp.Cookies())

	// Callback with a team group; the facade-backed resolver must turn the
	// derived team name into a real team ID in the session scope.
	code := provider.IssueCode(oidctest.TokenSpec{
		Subject: "user-1",
		Email:   "carol@example.com",
		Groups:  []string{"chetter-platform"},
		Nonce:   cookieValue(t, jar, auth.OAuthNonceCookieName),
	})
	callbackReq, err := http.NewRequest(http.MethodGet, server.URL+"/auth/callback?code="+code+"&state="+cookieValue(t, jar, auth.OAuthStateCookieName), nil)
	if err != nil {
		t.Fatalf("build callback request: %v", err)
	}
	for _, c := range jar {
		callbackReq.AddCookie(c)
	}
	callbackResp, err := newNoRedirectClient().Do(callbackReq)
	if err != nil {
		t.Fatalf("GET /auth/callback: %v", err)
	}
	callbackResp.Body.Close()
	if callbackResp.StatusCode != http.StatusFound {
		t.Fatalf("callback status = %d, want 302", callbackResp.StatusCode)
	}
	var sessionCookie *http.Cookie
	for _, c := range callbackResp.Cookies() {
		if c.Name == auth.SessionCookieName {
			sessionCookie = c
		}
	}
	if sessionCookie == nil {
		t.Fatal("callback did not set session cookie")
	}
	claims, err := a.ParseSession(sessionCookie.Value)
	if err != nil {
		t.Fatalf("parse session: %v", err)
	}
	if !claims.Scope().HasTeam("team-platform-id") {
		t.Errorf("session scope = %+v, want resolved team team-platform-id", claims.Scope())
	}
}
