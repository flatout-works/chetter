package auth

import "context"

type ctxKey struct{}

type Scope struct {
	TeamID string
	Admin  bool
}

func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

func GetScope(ctx context.Context) (Scope, bool) {
	s, ok := ctx.Value(ctxKey{}).(Scope)
	return s, ok
}
