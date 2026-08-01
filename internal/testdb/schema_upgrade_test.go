package testdb

import (
	"context"
	"strings"
	"testing"

	postgresmigrations "github.com/flatout-works/chetter/db/postgres/migrations"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/pressly/goose/v3"
)

func TestPostgresApplySchemaUpgradesVersionedDatabase(t *testing.T) {
	_, oldDB, cleanup := PostgresSchemaParityDBs(t)
	if oldDB == nil {
		return // skipped
	}
	defer cleanup()

	ctx := context.Background()
	provider, err := goose.NewProvider(goose.DialectPostgres, oldDB.DB, postgresmigrations.Files)
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	results, err := provider.DownTo(ctx, 22)
	if err != nil {
		t.Fatalf("downgrade schema to migration 22: %v", err)
	}
	if len(results) != 1 || results[0].Source.Version != 23 {
		t.Fatalf("downgrade results = %#v, want migration 23", results)
	}

	if _, err := oldDB.DB.ExecContext(ctx, `
		INSERT INTO api_tokens (id, name, token_hash, user_id, created_at, updated_at)
		VALUES ('token_upgrade_test', 'upgrade test', repeat('a', 64), 'user_upgrade_test', NOW(), NOW())
	`); err != nil {
		t.Fatalf("insert pre-upgrade token: %v", err)
	}

	firstStore, closeFirstStore := oldDB.OpenStore(t)
	defer closeFirstStore()
	secondStore, closeSecondStore := oldDB.OpenStore(t)
	defer closeSecondStore()

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, st := range []*store.Store{firstStore, secondStore} {
		go func() {
			<-start
			errs <- st.ApplySchema(ctx)
		}()
	}
	close(start)
	for range 2 {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent ApplySchema upgrade: %v", err)
		}
	}
	// A subsequent call exercises the normal restart path after all migrations
	// have already been applied.
	if err := firstStore.ApplySchema(ctx); err != nil {
		t.Fatalf("ApplySchema idempotent restart: %v", err)
	}

	var tokenID string
	var expiresIsNull bool
	if err := oldDB.DB.QueryRowContext(ctx, `
		SELECT id, expires_at IS NULL
		FROM api_tokens
		WHERE id = 'token_upgrade_test'
	`).Scan(&tokenID, &expiresIsNull); err != nil {
		t.Fatalf("query upgraded token: %v", err)
	}
	if tokenID != "token_upgrade_test" || !expiresIsNull {
		t.Fatalf("upgraded token = (%q, expires null %v)", tokenID, expiresIsNull)
	}
}

func TestPostgresApplySchemaRejectsUnversionedAndInitializesEmpty(t *testing.T) {
	bootstrapDB, _, cleanup := PostgresSchemaParityDBs(t)
	if bootstrapDB == nil {
		return // skipped
	}
	defer cleanup()

	st, closeStore := bootstrapDB.OpenStore(t)
	defer closeStore()
	err := st.ApplySchema(context.Background())
	if err == nil {
		t.Fatal("ApplySchema succeeded for an unversioned existing schema")
	}
	if !strings.Contains(err.Error(), "no goose_db_version table") {
		t.Fatalf("ApplySchema error = %q", err)
	}

	if _, err := bootstrapDB.DB.Exec(`DROP SCHEMA public CASCADE; CREATE SCHEMA public`); err != nil {
		t.Fatalf("reset to empty schema: %v", err)
	}
	if err := st.ApplySchema(context.Background()); err != nil {
		t.Fatalf("ApplySchema on empty database: %v", err)
	}
	var hasTasks, hasMigrationHistory bool
	if err := bootstrapDB.DB.QueryRow(`
		SELECT
			to_regclass('public.chetter_tasks') IS NOT NULL,
			to_regclass('public.goose_db_version') IS NOT NULL
	`).Scan(&hasTasks, &hasMigrationHistory); err != nil {
		t.Fatalf("inspect initialized schema: %v", err)
	}
	if !hasTasks || !hasMigrationHistory {
		t.Fatalf("initialized schema has tasks=%v migration history=%v", hasTasks, hasMigrationHistory)
	}
}
