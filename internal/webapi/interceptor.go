package webapi

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/flatout-works/chetter/internal/auth"
)

// authInterceptor implements connect.Interceptor, wrapping both unary
// and streaming handlers with authentication. Bearer tokens (admin MCP
// token or DB API tokens) are validated first; when OIDC is configured,
// a valid web session cookie is accepted as a fallback so the SPA can use
// SSO sessions alongside token-based access.
type authInterceptor struct {
	adminToken string
	db         *sql.DB
	oidc       *auth.OIDCAuth
}

func NewAuthInterceptor(adminToken string, db *sql.DB, oidc *auth.OIDCAuth) connect.Interceptor {
	return &authInterceptor{adminToken: adminToken, db: db, oidc: oidc}
}

func (a *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		scope, ok := a.resolve(ctx, req.Header())
		if !ok {
			return nil, connect.NewError(connect.CodeUnauthenticated, nil)
		}
		return next(auth.WithScope(ctx, scope), req)
	}
}

func (a *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		scope, ok := a.resolve(ctx, conn.RequestHeader())
		if !ok {
			return connect.NewError(connect.CodeUnauthenticated, nil)
		}
		return next(auth.WithScope(ctx, scope), conn)
	}
}

func (a *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (a *authInterceptor) resolve(ctx context.Context, h http.Header) (auth.Scope, bool) {
	scope, ok := auth.ResolveToken(ctx, a.adminToken, a.db, bearerToken(h))
	if ok {
		return scope, true
	}
	if a.oidc != nil {
		if sessionScope, ok := a.oidc.ScopeFromCookie(h); ok {
			return sessionScope, true
		}
	}
	return auth.Scope{}, false
}

func bearerToken(h http.Header) string {
	v := h.Get("Authorization")
	const prefix = "Bearer "
	if len(v) > len(prefix) && strings.HasPrefix(v, prefix) {
		return strings.TrimPrefix(v, prefix)
	}
	return ""
}
