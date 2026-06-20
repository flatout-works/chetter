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
// and streaming handlers with Bearer token validation.
type authInterceptor struct {
	adminToken string
	db         *sql.DB
}

func NewAuthInterceptor(adminToken string, db *sql.DB) connect.Interceptor {
	return &authInterceptor{adminToken: adminToken, db: db}
}

func (a *authInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		scope, ok := a.resolve(req.Header())
		if !ok {
			return nil, connect.NewError(connect.CodeUnauthenticated, nil)
		}
		return next(auth.WithScope(ctx, scope), req)
	}
}

func (a *authInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		scope, ok := a.resolve(conn.RequestHeader())
		if !ok {
			return connect.NewError(connect.CodeUnauthenticated, nil)
		}
		return next(auth.WithScope(ctx, scope), conn)
	}
}

func (a *authInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (a *authInterceptor) resolve(h http.Header) (auth.Scope, bool) {
	token := bearerToken(h)
	return auth.ResolveToken(context.Background(), a.adminToken, a.db, token)
}

func bearerToken(h http.Header) string {
	v := h.Get("Authorization")
	const prefix = "Bearer "
	if len(v) > len(prefix) && strings.HasPrefix(v, prefix) {
		return strings.TrimPrefix(v, prefix)
	}
	return ""
}
