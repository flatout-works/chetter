// Package auth OIDC support: OpenID Connect (OIDC) provider integration for
// web UI sessions (issue #94). Okta serves as the primary IdP but any
// OIDC-compliant provider works.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

// Cookie names used by the OIDC web flow.
const (
	SessionCookieName      = "chetter_session"
	OAuthStateCookieName   = "chetter_oauth_state"
	OAuthNonceCookieName   = "chetter_oauth_nonce"
	OAuthStateCookieMaxAge = 10 * 60 // 10 minutes
)

// Default group mapping values. Groups are mapped to Chetter scopes:
//   - the admin group grants full admin scope
//   - any group with the team prefix maps to a team with that name
//     (e.g. chetter-platform -> team "platform")
const (
	DefaultAdminGroup      = "chetter-admin"
	DefaultTeamGroupPrefix = "chetter-"
	DefaultSessionTTL      = 8 * time.Hour
)

// OIDCConfig holds the OIDC provider and session configuration. It is the
// auth-package view of the OIDC_* environment variables.
type OIDCConfig struct {
	IssuerURL       string
	ClientID        string
	ClientSecret    string
	RedirectURL     string
	AdminGroup      string
	TeamGroupPrefix string
	SessionSecret   string
	SessionTTL      time.Duration
}

// OIDCIdentity is the verified identity extracted from an ID token.
type OIDCIdentity struct {
	Subject       string
	Email         string
	EmailVerified bool
	Groups        []string
}

// SessionClaims is the signed session JWT carried in the web session cookie.
type SessionClaims struct {
	Subject string   `json:"sub"`
	Email   string   `json:"email"`
	Admin   bool     `json:"admin"`
	TeamIDs []string `json:"teams,omitempty"`
	IDToken string   `json:"id_token,omitempty"`
	jwt.RegisteredClaims
}

// Scope converts session claims back into an auth scope.
func (c SessionClaims) Scope() Scope {
	scope := Scope{Admin: c.Admin}
	if len(c.TeamIDs) > 0 {
		scope.TeamIDs = c.TeamIDs
		scope.TeamID = c.TeamIDs[0]
	}
	return scope
}

// OIDCAuth wires together an OIDC provider, the OAuth2 client config, and the
// stateless session JWT signer. A nil *OIDCAuth (or one built from an empty
// config) disables OIDC authentication entirely.
type OIDCAuth struct {
	cfg      OIDCConfig
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth2   *oauth2.Config
	signKey  []byte
}

// NewOIDCAuth performs provider discovery and builds the verifier. It returns
// an error if the provider cannot be reached or the config is incomplete.
func NewOIDCAuth(ctx context.Context, cfg OIDCConfig) (*OIDCAuth, error) {
	if strings.TrimSpace(cfg.IssuerURL) == "" ||
		strings.TrimSpace(cfg.ClientID) == "" ||
		strings.TrimSpace(cfg.ClientSecret) == "" ||
		strings.TrimSpace(cfg.RedirectURL) == "" {
		return nil, errors.New("oidc: issuer URL, client ID, client secret, and redirect URL are required")
	}
	if cfg.AdminGroup == "" {
		cfg.AdminGroup = DefaultAdminGroup
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = DefaultSessionTTL
	}
	if strings.TrimSpace(cfg.SessionSecret) == "" {
		return nil, errors.New("oidc: session secret is required")
	}
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc: provider discovery: %w", err)
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	signKey := []byte(cfg.SessionSecret)
	return &OIDCAuth{
		cfg:      cfg,
		provider: provider,
		verifier: verifier,
		oauth2: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "groups"},
		},
		signKey: signKey,
	}, nil
}

// SessionTTL returns the configured session lifetime.
func (a *OIDCAuth) SessionTTL() time.Duration {
	return a.cfg.SessionTTL
}

// RedirectOrigin returns the scheme://host of the configured redirect URL
// (OIDC_REDIRECT_URL). It is the externally visible origin registered with
// the IdP, so logout redirects never depend on request headers. It returns
// "" when the configured URL is missing or cannot be parsed.
func (a *OIDCAuth) RedirectOrigin() string {
	if a == nil {
		return ""
	}
	u, err := url.Parse(a.cfg.RedirectURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// TeamGroupPrefix returns the configured team group prefix ("chetter-" by
// default). Group-to-team mapping strips it to derive the team name.
func (a *OIDCAuth) TeamGroupPrefix() string {
	if a == nil {
		return DefaultTeamGroupPrefix
	}
	if a.cfg.TeamGroupPrefix == "" {
		return DefaultTeamGroupPrefix
	}
	return a.cfg.TeamGroupPrefix
}

// EndSessionEndpoint returns the IdP's end-session endpoint (Okta logout), if
// the provider advertises one.
func (a *OIDCAuth) EndSessionEndpoint() string {
	if a == nil || a.provider == nil {
		return ""
	}
	var metadata struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	if err := a.provider.Claims(&metadata); err != nil {
		return ""
	}
	return metadata.EndSessionEndpoint
}

// LoginURL builds the authorization URL for the given state and nonce.
func (a *OIDCAuth) LoginURL(state, nonce string) string {
	return a.oauth2.AuthCodeURL(state, oidc.Nonce(nonce))
}

// Exchange swaps an authorization code for an identity. The ID token is
// verified against the provider (signature, issuer, audience, and nonce).
// The raw ID token is returned so callers can pass it to the IdP's
// end-session endpoint on logout.
func (a *OIDCAuth) Exchange(ctx context.Context, code, nonce string) (*OIDCIdentity, string, error) {
	rawIDToken, err := a.exchangeIDToken(ctx, code)
	if err != nil {
		return nil, "", err
	}
	idToken, err := a.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, "", fmt.Errorf("oidc: verify id token: %w", err)
	}
	if nonce != "" {
		if err := verifyNonce(idToken, nonce); err != nil {
			return nil, "", err
		}
	}
	identity, err := identityFromIDToken(idToken)
	if err != nil {
		return nil, "", err
	}
	return identity, rawIDToken, nil
}

func (a *OIDCAuth) exchangeIDToken(ctx context.Context, code string) (string, error) {
	token, err := a.oauth2.Exchange(ctx, code)
	if err != nil {
		return "", fmt.Errorf("oidc: token exchange: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return "", errors.New("oidc: token exchange returned no id_token")
	}
	return rawIDToken, nil
}

func verifyNonce(idToken *oidc.IDToken, nonce string) error {
	var claims struct {
		Nonce string `json:"nonce"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return fmt.Errorf("oidc: read nonce claim: %w", err)
	}
	if claims.Nonce != nonce {
		return errors.New("oidc: id token nonce mismatch")
	}
	return nil
}

func identityFromIDToken(idToken *oidc.IDToken) (*OIDCIdentity, error) {
	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Groups        any    `json:"groups"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("oidc: read claims: %w", err)
	}
	identity := &OIDCIdentity{
		Subject: idToken.Subject,
		Email:   claims.Email,
	}
	if claims.EmailVerified != nil {
		identity.EmailVerified = *claims.EmailVerified
	}
	identity.Groups = stringGroups(claims.Groups)
	return identity, nil
}

// stringGroups normalizes the groups claim, which providers return either as
// a string array, a JSON array of arbitrary values, or a single string.
func stringGroups(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if v == "" {
			return nil
		}
		return []string{v}
	default:
		return nil
	}
}

// ScopeForGroups maps OIDC groups to a Chetter scope following the configured
// admin group and team group prefix. Team scopes carry the group-derived team
// names; the web API resolves them to real team IDs when present in the DB.
func (a *OIDCAuth) ScopeForGroups(groups []string) Scope {
	adminGroup := a.cfg.AdminGroup
	if adminGroup == "" {
		adminGroup = DefaultAdminGroup
	}
	prefix := a.cfg.TeamGroupPrefix
	scope := Scope{}
	for _, group := range groups {
		if group == "" {
			continue
		}
		if group == adminGroup {
			scope.Admin = true
			continue
		}
		if prefix != "" && strings.HasPrefix(group, prefix) {
			team := strings.TrimPrefix(group, prefix)
			if team != "" {
				scope.TeamIDs = append(scope.TeamIDs, team)
			}
		}
	}
	if len(scope.TeamIDs) > 0 {
		scope.TeamID = scope.TeamIDs[0]
	}
	return scope
}

// NewSession mints a short-lived session JWT for the given identity and scope.
// The raw ID token is embedded so logout can pass id_token_hint to the IdP.
func (a *OIDCAuth) NewSession(identity *OIDCIdentity, scope Scope, rawIDToken string) (string, error) {
	now := time.Now().UTC()
	claims := SessionClaims{
		Subject: identity.Subject,
		Email:   identity.Email,
		Admin:   scope.Admin,
		TeamIDs: scope.Teams(),
		IDToken: rawIDToken,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   identity.Subject,
			Issuer:    "chetter-web",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(a.cfg.SessionTTL)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(a.signKey)
	if err != nil {
		return "", fmt.Errorf("oidc: sign session: %w", err)
	}
	return signed, nil
}

// ParseSession validates a session JWT and returns its claims.
func (a *OIDCAuth) ParseSession(sessionToken string) (*SessionClaims, error) {
	var claims SessionClaims
	token, err := jwt.ParseWithClaims(sessionToken, &claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("oidc: unexpected session signing method %q", t.Method.Alg())
		}
		return a.signKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("oidc: parse session: %w", err)
	}
	if !token.Valid {
		return nil, errors.New("oidc: invalid session token")
	}
	return &claims, nil
}

// SessionFromCookie extracts and validates the session cookie from an HTTP
// header set. ok is false when the cookie is absent or invalid/expired.
func (a *OIDCAuth) SessionFromCookie(header http.Header) (*SessionClaims, bool) {
	if a == nil {
		return nil, false
	}
	session := cookieValue(header, SessionCookieName)
	if session == "" {
		return nil, false
	}
	claims, err := a.ParseSession(session)
	if err != nil {
		return nil, false
	}
	return claims, true
}

// ScopeFromCookie extracts and validates the session cookie from an HTTP
// header set and returns the session scope. ok is false when the cookie is
// absent or the session is invalid/expired.
func (a *OIDCAuth) ScopeFromCookie(header http.Header) (Scope, bool) {
	claims, ok := a.SessionFromCookie(header)
	if !ok {
		return Scope{}, false
	}
	return claims.Scope(), true
}

func cookieValue(header http.Header, name string) string {
	if header == nil {
		return ""
	}
	req := &http.Request{Header: header}
	cookie, err := req.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}
