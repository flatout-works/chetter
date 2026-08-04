package webapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/repository"
)

// TeamResolver is the team lookup surface the OIDC handlers need. It is
// satisfied by the dialect-agnostic data facade (internal/data), so
// group-to-team mapping goes through the generated dual-dialect queries
// instead of hand-rolled SQL with dialect fallbacks.
type TeamResolver interface {
	// ListTeams returns all teams, used to resolve group-derived names to
	// team IDs and to apply okta_group_id/okta_group_name overrides.
	ListTeams(ctx context.Context) ([]repository.Team, error)
}

// RegisterOIDCRoutes mounts the web UI OIDC login flow. It is a no-op when
// oidc is nil (OIDC not configured). teams may be nil; group-derived team
// names are then kept as-is.
//
//	GET /auth/login    — redirect to the IdP authorization endpoint
//	GET /auth/callback — exchange the code, mint a session, set the cookie
//	GET/POST /auth/logout — clear the session cookie, redirect to IdP logout
//	GET /auth/session  — report session state to the SPA (200/401)
func RegisterOIDCRoutes(mux *http.ServeMux, oidc *auth.OIDCAuth, teams TeamResolver) {
	if oidc == nil {
		return
	}
	h := &oidcHandlers{oidc: oidc, teams: teams}
	mux.HandleFunc("GET /auth/login", h.handleLogin)
	mux.HandleFunc("GET /auth/callback", h.handleCallback)
	mux.HandleFunc("GET /auth/logout", h.handleLogout)
	mux.HandleFunc("POST /auth/logout", h.handleLogout)
	mux.HandleFunc("GET /auth/session", h.handleSession)
}

type oidcHandlers struct {
	oidc  *auth.OIDCAuth
	teams TeamResolver
}

// handleLogin starts the authorization code flow: it sets a short-lived
// state cookie (CSRF protection) plus a nonce cookie and redirects to the
// IdP.
func (h *oidcHandlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	state, err := randomToken(32)
	if err != nil {
		slog.Error("oidc login: generate state", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonce, err := randomToken(32)
	if err != nil {
		slog.Error("oidc login: generate nonce", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	secure := isHTTPS(r)
	http.SetCookie(w, &http.Cookie{
		Name:     auth.OAuthStateCookieName,
		Value:    state,
		Path:     "/auth",
		MaxAge:   auth.OAuthStateCookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     auth.OAuthNonceCookieName,
		Value:    nonce,
		Path:     "/auth",
		MaxAge:   auth.OAuthStateCookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
	http.Redirect(w, r, h.oidc.LoginURL(state, nonce), http.StatusFound)
}

// handleCallback completes the authorization code flow: it validates the
// state cookie, exchanges the code for a verified identity, maps the IdP
// groups to a Chetter scope, and issues the session cookie.
func (h *oidcHandlers) handleCallback(w http.ResponseWriter, r *http.Request) {
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		slog.Warn("oidc callback: provider error", "error", errMsg, "description", r.URL.Query().Get("error_description"))
		http.Error(w, "authentication failed", http.StatusBadRequest)
		return
	}
	stateCookie, err := r.Cookie(auth.OAuthStateCookieName)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		slog.Warn("oidc callback: state mismatch")
		http.Error(w, "invalid state", http.StatusBadRequest)
		return
	}
	nonce := ""
	if nonceCookie, err := r.Cookie(auth.OAuthNonceCookieName); err == nil {
		nonce = nonceCookie.Value
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}

	identity, rawIDToken, err := h.oidc.Exchange(r.Context(), code, nonce)
	if err != nil {
		slog.Warn("oidc callback: exchange failed", "error", err)
		http.Error(w, "authentication failed", http.StatusBadRequest)
		return
	}

	scope := h.oidc.ScopeForGroups(identity.Groups)
	scope.TeamIDs = h.resolveTeamIDs(r.Context(), identity.Groups, scope.TeamIDs)
	if len(scope.TeamIDs) > 0 {
		scope.TeamID = scope.TeamIDs[0]
	}

	session, err := h.oidc.NewSession(identity, scope, rawIDToken)
	if err != nil {
		slog.Error("oidc callback: mint session", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.clearOAuthCookies(w, r)
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    session,
		Path:     "/",
		MaxAge:   int(h.oidc.SessionTTL().Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
	slog.Info("oidc: session established", "subject", identity.Subject, "email", identity.Email)
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleLogout clears the session cookie and redirects to the IdP's
// end-session endpoint (when advertised) so the SSO session is terminated
// too. post_logout_redirect_uri sends the browser back to the app.
func (h *oidcHandlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	rawIDToken := ""
	if claims, ok := h.oidc.SessionFromCookie(r.Header); ok {
		rawIDToken = claims.IDToken
	}
	h.clearSessionCookie(w, r)
	endSession := h.oidc.EndSessionEndpoint()
	if endSession != "" {
		u, err := url.Parse(endSession)
		if err == nil {
			q := u.Query()
			if rawIDToken != "" {
				q.Set("id_token_hint", rawIDToken)
			}
			q.Set("post_logout_redirect_uri", h.logoutRedirectURI(r))
			u.RawQuery = q.Encode()
			http.Redirect(w, r, u.String(), http.StatusFound)
			return
		}
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// logoutRedirectURI returns the absolute URL the IdP should send the browser
// back to after logout. It is derived from the configured OIDC_REDIRECT_URL
// origin (guaranteed to be registered with the IdP) rather than from request
// headers, which may be spoofed or rewritten by proxies. The request-based
// fallback only applies when the configured URL cannot be parsed.
func (h *oidcHandlers) logoutRedirectURI(r *http.Request) string {
	origin := ""
	if h.oidc != nil {
		origin = h.oidc.RedirectOrigin()
	}
	if origin == "" {
		return appBaseURL(r) + "/"
	}
	return origin + "/"
}

// handleSession reports whether the request carries a valid session cookie.
// The SPA calls this to decide between showing the app and redirecting to
// /auth/login.
func (h *oidcHandlers) handleSession(w http.ResponseWriter, r *http.Request) {
	claims, ok := h.oidc.SessionFromCookie(r.Header)
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"authenticated":false}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"authenticated": true,
		"email":         claims.Email,
		"admin":         claims.Admin,
		"teams":         claims.TeamIDs,
	})
}

func (h *oidcHandlers) clearOAuthCookies(w http.ResponseWriter, r *http.Request) {
	secure := isHTTPS(r)
	for _, name := range []string{auth.OAuthStateCookieName, auth.OAuthNonceCookieName} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/auth",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   secure,
		})
	}
}

func (h *oidcHandlers) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.SessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r),
	})
}

// resolveTeamIDs maps group-derived team names to real team IDs via the
// teams table. A team whose okta_group_id or okta_group_name matches one of
// the user's groups exactly wins over the prefix-based name mapping (issue
// #94 okta override). Names that do not resolve are kept as-is (the issue's
// literal mapping chetter-<name> -> TeamID "<name>"), which safely yields
// an empty result set for unknown teams.
func (h *oidcHandlers) resolveTeamIDs(ctx context.Context, groups, teamNames []string) []string {
	if h.teams == nil || len(teamNames) == 0 && len(groups) == 0 {
		return teamNames
	}
	all, err := h.teams.ListTeams(ctx)
	if err != nil {
		slog.Warn("oidc: list teams for group mapping", "error", err)
		return teamNames
	}
	byName := make(map[string]string, len(all))
	byOkta := make(map[string]string, len(all))
	for _, t := range all {
		if t.Name != "" {
			byName[t.Name] = t.ID
		}
		if t.OktaGroupID.Valid && t.OktaGroupID.String != "" {
			byOkta[t.OktaGroupID.String] = t.ID
		}
		if t.OktaGroupName.Valid && t.OktaGroupName.String != "" {
			byOkta[t.OktaGroupName.String] = t.ID
		}
	}
	resolved := make([]string, 0, len(teamNames))
	seen := make(map[string]bool, len(teamNames)+len(groups))
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		resolved = append(resolved, id)
	}
	// Exact okta_group_id / okta_group_name bindings are the most specific
	// match and win over the prefix-derived team name.
	for _, g := range groups {
		if g == "" {
			continue
		}
		if id, ok := byOkta[g]; ok {
			add(id)
		}
	}
	prefix := ""
	if h.oidc != nil {
		prefix = h.oidc.TeamGroupPrefix()
	}
	for _, name := range teamNames {
		if name == "" {
			continue
		}
		if prefix != "" {
			if _, ok := byOkta[prefix+name]; ok {
				continue // group already added via the exact okta binding
			}
		}
		if id, ok := byName[name]; ok {
			add(id)
		} else {
			add(name) // literal mapping for unknown teams
		}
	}
	return resolved
}

// randomToken returns a hex-encoded cryptographically random token.
func randomToken(bytes int) (string, error) {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// isHTTPS reports whether the request arrived over TLS, honoring the
// X-Forwarded-Proto header used by reverse proxies.
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto != "" && !strings.EqualFold(proto, "http") {
		return true
	}
	return r.URL.Scheme == "https"
}

// appBaseURL reconstructs the externally visible base URL of the web app so
// post_logout_redirect_uri is absolute (Okta requirement).
func appBaseURL(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	return scheme + "://" + host
}
