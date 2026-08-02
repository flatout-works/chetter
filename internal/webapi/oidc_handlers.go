package webapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/flatout-works/chetter/internal/auth"
)

// RegisterOIDCRoutes mounts the web UI OIDC login flow. It is a no-op when
// oidc is nil (OIDC not configured).
//
//	GET /auth/login    — redirect to the IdP authorization endpoint
//	GET /auth/callback — exchange the code, mint a session, set the cookie
//	GET/POST /auth/logout — clear the session cookie, redirect to IdP logout
//	GET /auth/session  — report session state to the SPA (200/401)
func RegisterOIDCRoutes(mux *http.ServeMux, oidc *auth.OIDCAuth, db *sql.DB) {
	if oidc == nil {
		return
	}
	h := &oidcHandlers{oidc: oidc, db: db}
	mux.HandleFunc("GET /auth/login", h.handleLogin)
	mux.HandleFunc("GET /auth/callback", h.handleCallback)
	mux.HandleFunc("GET /auth/logout", h.handleLogout)
	mux.HandleFunc("POST /auth/logout", h.handleLogout)
	mux.HandleFunc("GET /auth/session", h.handleSession)
}

type oidcHandlers struct {
	oidc *auth.OIDCAuth
	db   *sql.DB
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
	scope.TeamIDs = resolveTeamIDs(r.Context(), h.db, scope.TeamIDs)
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
			q.Set("post_logout_redirect_uri", appBaseURL(r)+"/")
			u.RawQuery = q.Encode()
			http.Redirect(w, r, u.String(), http.StatusFound)
			return
		}
	}
	http.Redirect(w, r, "/", http.StatusFound)
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
// teams table. Names that do not resolve are kept as-is (the issue's
// literal mapping chetter-<name> -> TeamID "<name>"), which safely yields an
// empty result set for unknown teams.
func resolveTeamIDs(ctx context.Context, db *sql.DB, teamNames []string) []string {
	if db == nil || len(teamNames) == 0 {
		return teamNames
	}
	resolved := make([]string, 0, len(teamNames))
	for _, name := range teamNames {
		if name == "" {
			continue
		}
		id := lookupTeamIDByName(ctx, db, name)
		if id == "" {
			id = name
		}
		resolved = append(resolved, id)
	}
	return resolved
}

func lookupTeamIDByName(ctx context.Context, db *sql.DB, name string) string {
	// The teams table is not part of the generated repository surface; use
	// the same ? -> $1 fallback as auth.lookupTokenScope for PostgreSQL.
	const query = `SELECT id FROM teams WHERE name = ?`
	var id string
	err := db.QueryRowContext(ctx, query, name).Scan(&id)
	if err != nil {
		err = db.QueryRowContext(ctx, strings.Replace(query, "?", "$1", 1), name).Scan(&id)
	}
	if err != nil {
		return ""
	}
	return id
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
