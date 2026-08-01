package store

import (
	"context"
	"fmt"

	postgresmigrations "github.com/flatout-works/chetter/db/postgres/migrations"
	"github.com/flatout-works/chetter/internal/dbmigration"
)

// applyPostgresMigrations upgrades an existing, Goose-versioned PostgreSQL
// database before the bootstrap DDL runs. PostgreSQL migrations include data
// movement and destructive ownership cutovers that cannot safely be inferred
// from CREATE TABLE IF NOT EXISTS statements.
func (s *Store) applyPostgresMigrations(ctx context.Context) error {
	versioned, hasApplicationSchema, err := s.postgresSchemaState(ctx)
	if err != nil {
		return err
	}
	if hasApplicationSchema && !versioned {
		return fmt.Errorf("postgres schema contains Chetter tables but no goose_db_version table; refusing to infer an unsafe migration baseline")
	}

	return dbmigration.UpPostgres(ctx, s.db, postgresmigrations.Files)
}

func (s *Store) postgresSchemaState(ctx context.Context) (versioned, hasApplicationSchema bool, err error) {
	if err := s.db.QueryRowContext(ctx, `
		SELECT
			to_regclass(current_schema() || '.goose_db_version') IS NOT NULL,
			EXISTS (
				SELECT 1
				FROM information_schema.tables
				WHERE table_schema = current_schema()
				  AND table_name IN ('chetter_tasks', 'teams', 'api_tokens')
			)
	`).Scan(&versioned, &hasApplicationSchema); err != nil {
		return false, false, fmt.Errorf("inspect postgres schema migration state: %w", err)
	}
	return versioned, hasApplicationSchema, nil
}
