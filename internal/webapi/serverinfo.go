package webapi

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"time"

	"github.com/flatout-works/chetter/internal/auth"
)

// ServerInfoConfig carries the providers the server-info endpoint needs.
// Function fields keep the handler decoupled from main's globals so it stays
// unit-testable.
type ServerInfoConfig struct {
	AdminToken      string
	DB              *sql.DB
	OIDC            *auth.OIDCAuth
	Version         func() string
	GitHash         func() string
	UptimeSeconds   func() int64
	StartedAt       func() time.Time
	QuotaExhausted  func() bool
	LastReapAt      func() time.Time
	AllowTokenLogin bool
	DBSessionTZ     string
	DBGlobalTZ      string
	DBTimeZoneUTC   bool
}

// NewServerInfoHandler builds GET /api/server-info. The endpoint must stay
// reachable without credentials because the SPA reads oidcEnabled and
// allowTokenLogin from it before making any login decision. Everything else
// — build identity, uptime, database posture — is only returned to
// authenticated callers (admin/team bearer token or OIDC session cookie),
// mirroring the ConnectRPC auth interceptor.
func NewServerInfoHandler(cfg ServerInfoConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		public := map[string]any{
			"oidcEnabled":     cfg.OIDC != nil,
			"allowTokenLogin": cfg.AllowTokenLogin,
		}
		payload := public
		if serverInfoAuthed(r, cfg) {
			full := make(map[string]any, len(public)+9)
			for k, v := range public {
				full[k] = v
			}
			lastReap := cfg.LastReapAt()
			var lastReapField any
			if !lastReap.IsZero() {
				lastReapField = lastReap.UTC().Format(time.RFC3339Nano)
			}
			full["serverVersion"] = cfg.Version()
			full["gitHash"] = cfg.GitHash()
			full["uptimeSeconds"] = cfg.UptimeSeconds()
			full["startedAt"] = cfg.StartedAt().UTC().Format(time.RFC3339)
			full["quotaExhausted"] = cfg.QuotaExhausted()
			full["lastReapAt"] = lastReapField
			full["dbSessionTimeZone"] = cfg.DBSessionTZ
			full["dbGlobalTimeZone"] = cfg.DBGlobalTZ
			full["dbTimeZoneUTC"] = cfg.DBTimeZoneUTC
			payload = full
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(payload)
	}
}

// serverInfoAuthed reports whether the request carries a valid admin or team
// bearer token, or a valid OIDC session cookie.
func serverInfoAuthed(r *http.Request, cfg ServerInfoConfig) bool {
	if _, ok := auth.ResolveToken(r.Context(), cfg.AdminToken, cfg.DB, bearerToken(r.Header)); ok {
		return true
	}
	if cfg.OIDC != nil {
		if _, ok := cfg.OIDC.ScopeFromCookie(r.Header); ok {
			return true
		}
	}
	return false
}
