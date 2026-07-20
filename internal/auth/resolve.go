package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
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
	tokenQuery := `
		SELECT t.id, t.name, u.team_id
		FROM api_tokens t
		JOIN users u ON u.id = t.user_id
		WHERE t.token_hash = ?`
	err := db.QueryRowContext(ctx, tokenQuery, tokenHash).Scan(&tokenID, &tokenName, &fallbackTeamID)
	if err != nil {
		// api_token_teams is not part of the generated repository surface. Use
		// PostgreSQL placeholders for this small authentication lookup instead
		// of relying on a driver-level SQL rewriter.
		err = db.QueryRowContext(ctx, strings.Replace(tokenQuery, "?", "$1", 1), tokenHash).Scan(&tokenID, &tokenName, &fallbackTeamID)
	}
	if err != nil {
		return Scope{}
	}
	teamQuery := `
		SELECT team_id
		FROM api_token_teams
		WHERE token_id = ?
		ORDER BY team_id ASC`
	rows, err := db.QueryContext(ctx, teamQuery, tokenID.String)
	if err != nil {
		rows, err = db.QueryContext(ctx, strings.Replace(teamQuery, "?", "$1", 1), tokenID.String)
	}
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
