package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
)

// ResolveToken validates a raw bearer token against the admin token
// and the api_tokens table. Returns the scope and true if valid.
func ResolveToken(ctx context.Context, adminToken string, db *sql.DB, rawToken string) (Scope, bool) {
	if adminToken != "" && rawToken == adminToken {
		return Scope{Admin: true}, true
	}
	if db != nil {
		scope := lookupTokenScope(ctx, db, rawToken)
		if len(scope.Teams()) > 0 {
			return scope, true
		}
	}
	return Scope{}, false
}

func lookupTokenScope(ctx context.Context, db *sql.DB, rawToken string) Scope {
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])
	var tokenID, tokenName, fallbackTeamID sql.NullString
	err := db.QueryRowContext(ctx, `
		SELECT t.id, t.name, u.team_id
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ?`, tokenHash).Scan(&tokenID, &tokenName, &fallbackTeamID)
	if err != nil {
		return Scope{}
	}
	rows, err := db.QueryContext(ctx, `
		SELECT team_id
		FROM api_token_teams
		WHERE token_id = ?
		ORDER BY team_id ASC`, tokenID.String)
	if err != nil {
		return Scope{TokenID: tokenID.String, TokenName: tokenName.String, TeamID: fallbackTeamID.String, TeamIDs: []string{fallbackTeamID.String}}
	}
	defer rows.Close()
	var teamIDs []string
	for rows.Next() {
		var teamID string
		if err := rows.Scan(&teamID); err == nil && teamID != "" {
			teamIDs = append(teamIDs, teamID)
		}
	}
	if len(teamIDs) == 0 && fallbackTeamID.Valid && fallbackTeamID.String != "" {
		teamIDs = []string{fallbackTeamID.String}
	}
	if len(teamIDs) == 0 {
		return Scope{}
	}
	return Scope{TokenID: tokenID.String, TokenName: tokenName.String, TeamID: teamIDs[0], TeamIDs: teamIDs}
}
