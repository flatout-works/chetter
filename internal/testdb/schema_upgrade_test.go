package testdb

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

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
	latest := latestPostgresMigrationVersion(t)
	results, err := provider.DownTo(ctx, latest-1)
	if err != nil {
		t.Fatalf("downgrade schema to migration %d: %v", latest-1, err)
	}
	if len(results) != 1 || results[0].Source.Version != latest {
		t.Fatalf("downgrade results = %#v, want migration %d", results, latest)
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

func latestPostgresMigrationVersion(t *testing.T) int64 {
	t.Helper()
	entries, err := fs.ReadDir(postgresmigrations.Files, ".")
	if err != nil {
		t.Fatalf("read embedded postgres migrations: %v", err)
	}
	var latest int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		versionText := strings.SplitN(filepath.Base(entry.Name()), "_", 2)[0]
		version, err := strconv.ParseInt(versionText, 10, 64)
		if err != nil {
			t.Fatalf("parse migration version from %q: %v", entry.Name(), err)
		}
		if version > latest {
			latest = version
		}
	}
	if latest == 0 {
		t.Fatal("no embedded postgres migrations")
	}
	return latest
}

// preRenameTables matches the pre-052 table names on word boundaries. The \b
// after "chetter_" excludes index names such as idx_chetter_tasks_* (the "_"
// before "chetter_" is a word character, so there is no boundary there).
var preRenameTables = regexp.MustCompile(`\bchetter_(agent_session_checkpoints|agent_sessions|audit_log|event_callbacks|execution_attempts|model_catalogs|runners|task_artifacts|task_events|trigger_runs|triggers|user_prompts|webhook_deliveries|tasks)\b`)

// translatePreRenameMigration rewrites pre-052 table names in a historical
// migration to their current names so it can be replayed against the
// post-rename schema. Only the table names change; the migration's structure
// (e.g. one ALTER per column for TiDB) is preserved.
func translatePreRenameMigration(sql string) string {
	return preRenameTables.ReplaceAllString(sql, "$1")
}

func TestMySQLApplyTaskGitHubMetadataMigration(t *testing.T) {
	if store.ParseDialect(os.Getenv("CHETTER_TEST_DB_DIALECT")) == store.DialectPostgres {
		t.Skip("MySQL/TiDB migration test")
	}
	tdb, cleanup := NewForTesting(t)
	defer cleanup()

	ctx := context.Background()
	if _, err := tdb.DB.ExecContext(ctx, "ALTER TABLE tasks DROP COLUMN github_installation_id, DROP COLUMN github_repo"); err != nil {
		t.Fatalf("drop GitHub metadata columns: %v", err)
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate migration test source")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "../../db/migrations/048_add_task_github_metadata.sql"))
	if err != nil {
		t.Fatalf("read GitHub metadata migration: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectMySQL, tdb.DB, fstest.MapFS{
		"048_add_task_github_metadata.sql": &fstest.MapFile{Data: []byte(translatePreRenameMigration(string(migration)))},
	})
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("apply GitHub metadata migration: %v", err)
	}

	rows, err := tdb.DB.QueryContext(ctx, "SELECT github_repo, github_installation_id FROM tasks WHERE 1=0")
	if err != nil {
		t.Fatalf("query migrated GitHub metadata columns: %v", err)
	}
	rows.Close()
}

func TestMySQLApplySelfTestMetadataMigration(t *testing.T) {
	if store.ParseDialect(os.Getenv("CHETTER_TEST_DB_DIALECT")) == store.DialectPostgres {
		t.Skip("MySQL/TiDB migration test")
	}
	tdb, cleanup := NewForTesting(t)
	defer cleanup()

	ctx := context.Background()
	if _, err := tdb.DB.ExecContext(ctx, "DROP INDEX idx_chetter_tasks_self_test_run ON tasks"); err != nil {
		t.Fatalf("drop self-test run index: %v", err)
	}
	if _, err := tdb.DB.ExecContext(ctx, "ALTER TABLE tasks DROP COLUMN self_test_nonce, DROP COLUMN self_test_check, DROP COLUMN self_test_profile, DROP COLUMN self_test_run_id"); err != nil {
		t.Fatalf("drop self-test metadata columns: %v", err)
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate migration test source")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "../../db/migrations/050_add_task_self_test_metadata.sql"))
	if err != nil {
		t.Fatalf("read self-test metadata migration: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectMySQL, tdb.DB, fstest.MapFS{
		"050_add_task_self_test_metadata.sql": &fstest.MapFile{Data: []byte(translatePreRenameMigration(string(migration)))},
	})
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("apply self-test metadata migration: %v", err)
	}

	rows, err := tdb.DB.QueryContext(ctx, "SELECT self_test_run_id, self_test_profile, self_test_check, self_test_nonce FROM tasks WHERE 1=0")
	if err != nil {
		t.Fatalf("query migrated self-test metadata columns: %v", err)
	}
	rows.Close()
}

func TestMySQLApplyIsolationColumnsMigration(t *testing.T) {
	if store.ParseDialect(os.Getenv("CHETTER_TEST_DB_DIALECT")) == store.DialectPostgres {
		t.Skip("MySQL/TiDB migration test")
	}
	tdb, cleanup := NewForTesting(t)
	defer cleanup()

	ctx := context.Background()
	// Drop the columns added by migration 051 so we can prove the migration
	// re-adds them (TiDB rejects multi-column ALTERs with cross-referencing
	// AFTER clauses, so the migration uses one ALTER per column — issue #291).
	if _, err := tdb.DB.ExecContext(ctx, "ALTER TABLE runners DROP COLUMN isolation_enabled"); err != nil {
		t.Fatalf("drop isolation_enabled: %v", err)
	}
	if _, err := tdb.DB.ExecContext(ctx, "ALTER TABLE agent_sessions DROP COLUMN isolation_required"); err != nil {
		t.Fatalf("drop isolation_required: %v", err)
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate migration test source")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "../../db/migrations/051_add_isolation_columns.sql"))
	if err != nil {
		t.Fatalf("read isolation columns migration: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectMySQL, tdb.DB, fstest.MapFS{
		"051_add_isolation_columns.sql": &fstest.MapFile{Data: []byte(translatePreRenameMigration(string(migration)))},
	})
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("apply isolation columns migration: %v", err)
	}

	rows, err := tdb.DB.QueryContext(ctx, "SELECT isolation_required FROM agent_sessions WHERE 1=0")
	if err != nil {
		t.Fatalf("query migrated isolation_required column: %v", err)
	}
	rows.Close()
	rows, err = tdb.DB.QueryContext(ctx, "SELECT isolation_enabled FROM runners WHERE 1=0")
	if err != nil {
		t.Fatalf("query migrated isolation_enabled column: %v", err)
	}
	rows.Close()
}

// TestMySQLApplyMultiReplicaCoordinationMigration drops the coordination
// tables created by the bootstrap schema and proves migration 054 re-creates
// them (including the claim_notify_counter and admission_locks seed rows) —
// the path an existing goose-managed deployment takes on upgrade.
func TestMySQLApplyMultiReplicaCoordinationMigration(t *testing.T) {
	if store.ParseDialect(os.Getenv("CHETTER_TEST_DB_DIALECT")) == store.DialectPostgres {
		t.Skip("MySQL/TiDB migration test")
	}
	tdb, cleanup := NewForTesting(t)
	defer cleanup()

	ctx := context.Background()
	for _, table := range []string{"claim_notify_counter", "trigger_locks", "admission_locks", "runner_drain_requests"} {
		if _, err := tdb.DB.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop %s: %v", table, err)
		}
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate migration test source")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(sourceFile), "../../db/migrations/054_add_multi_replica_coordination.sql"))
	if err != nil {
		t.Fatalf("read coordination migration: %v", err)
	}
	provider, err := goose.NewProvider(goose.DialectMySQL, tdb.DB, fstest.MapFS{
		"054_add_multi_replica_coordination.sql": &fstest.MapFile{Data: []byte(string(migration))},
	})
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("apply coordination migration: %v", err)
	}

	for _, table := range []string{"trigger_locks", "admission_locks", "runner_drain_requests"} {
		rows, err := tdb.DB.QueryContext(ctx, "SELECT * FROM "+table+" WHERE 1=0")
		if err != nil {
			t.Fatalf("query migrated %s: %v", table, err)
		}
		rows.Close()
	}
	var counter int64
	if err := tdb.DB.QueryRowContext(ctx, "SELECT counter FROM claim_notify_counter WHERE id = 1").Scan(&counter); err != nil {
		t.Fatalf("query claim_notify_counter seed row: %v", err)
	}
	if counter != 0 {
		t.Fatalf("claim_notify_counter seed counter = %d, want 0", counter)
	}
	var admissionName string
	if err := tdb.DB.QueryRowContext(ctx, "SELECT name FROM admission_locks WHERE name = 'pending_tasks'").Scan(&admissionName); err != nil {
		t.Fatalf("query admission_locks seed row: %v", err)
	}
	if admissionName != "pending_tasks" {
		t.Fatalf("admission_locks seed name = %q", admissionName)
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
			to_regclass('public.tasks') IS NOT NULL,
			to_regclass('public.goose_db_version') IS NOT NULL
	`).Scan(&hasTasks, &hasMigrationHistory); err != nil {
		t.Fatalf("inspect initialized schema: %v", err)
	}
	if !hasTasks || !hasMigrationHistory {
		t.Fatalf("initialized schema has tasks=%v migration history=%v", hasTasks, hasMigrationHistory)
	}
}
