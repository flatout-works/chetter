package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"

	"github.com/flatout-works/chetter/internal/repository"
)

// ResolveToken validates a raw bearer token against the admin token
// and the api_tokens table. Returns the scope and true if valid.
func ResolveToken(_ context.Context, adminToken string, db *sql.DB, rawToken string) (Scope, bool) {
	if adminToken != "" && rawToken == adminToken {
		return Scope{Admin: true}, true
	}
	if db != nil {
		scope := lookupTokenScope(db, rawToken)
		if scope.TeamID != "" {
			return scope, true
		}
	}
	return Scope{}, false
}

func lookupTokenScope(db *sql.DB, rawToken string) Scope {
	hash := sha256.Sum256([]byte(rawToken))
	tokenHash := hex.EncodeToString(hash[:])
	repo := repository.New(db)
	row, err := repo.GetTokenByHash(context.Background(), tokenHash)
	if err != nil {
		return Scope{}
	}
	return Scope{TeamID: row.TeamID}
}
