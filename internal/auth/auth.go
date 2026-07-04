package auth

import "context"

type ctxKey struct{}

type Scope struct {
	TeamID  string
	TeamIDs []string
	Admin   bool
}

func WithScope(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

func GetScope(ctx context.Context) (Scope, bool) {
	s, ok := ctx.Value(ctxKey{}).(Scope)
	return s, ok
}

func (s Scope) Teams() []string {
	if len(s.TeamIDs) > 0 {
		return s.TeamIDs
	}
	if s.TeamID != "" {
		return []string{s.TeamID}
	}
	return nil
}

func (s Scope) HasTeam(teamID string) bool {
	if teamID == "" {
		return false
	}
	for _, id := range s.Teams() {
		if id == teamID {
			return true
		}
	}
	return false
}
