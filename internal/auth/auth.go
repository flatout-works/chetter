package auth

import "context"

type ctxKey struct{}

type Scope struct {
	TeamID    string
	TeamIDs   []string
	TokenID   string
	TokenName string
	Admin     bool
}

// TeamFilter describes the effective team filter for a request. An
// unconstrained filter is reserved for admins and callers without an auth
// scope; a scoped filter with no IDs means the request must return no rows.
type TeamFilter struct {
	TeamIDs     []string
	Constrained bool
	Empty       bool
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

// ResolveTeamFilter applies a requested team filter to the caller's scope.
// For non-admin callers, an empty intersection is an explicit no-result
// filter, never an instruction to omit the team predicate.
func ResolveTeamFilter(ctx context.Context, requested []string) TeamFilter {
	scope, scoped := GetScope(ctx)
	requested = uniqueNonEmpty(requested)
	if !scoped || scope.Admin {
		if len(requested) == 0 {
			return TeamFilter{}
		}
		return TeamFilter{TeamIDs: requested, Constrained: true}
	}

	allowed := uniqueNonEmpty(scope.Teams())
	if len(requested) == 0 {
		return TeamFilter{TeamIDs: allowed, Constrained: true, Empty: len(allowed) == 0}
	}

	allowedSet := make(map[string]struct{}, len(allowed))
	for _, teamID := range allowed {
		allowedSet[teamID] = struct{}{}
	}
	intersection := make([]string, 0, len(requested))
	for _, teamID := range requested {
		if _, ok := allowedSet[teamID]; ok {
			intersection = append(intersection, teamID)
		}
	}
	if len(intersection) == 0 {
		return TeamFilter{Constrained: true, Empty: true}
	}
	return TeamFilter{TeamIDs: intersection, Constrained: true}
}

func uniqueNonEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
