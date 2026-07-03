// Package testdb spins up a real database instance (TiDB or MySQL) for
// integration tests.
//
// Tests use CHETTER_TEST_DSN to point at an existing database (e.g. a CI
// service container). When that env var is unset, the package starts a
// one-shot Docker container for the test run and tears it down on exit.
//
// Set CHETTER_TEST_DB_DIALECT to "mysql" to use a MySQL container instead of
// the default TiDB container.
package testdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/store"
	"github.com/go-sql-driver/mysql"
)

const (
	defaultTiDBImage  = "pingcap/tidb:v8.5.1"
	defaultMySQLImage = "mysql:8.0"
	connectTimeout    = 30 * time.Second
	testMaxOpenConns  = 10
	testMaxIdleConns  = 5
)

// TestDB owns a connection to a real database instance plus the cleanup hooks
// the test should defer.
type TestDB struct {
	DB       *sql.DB
	DSN      string
	database string
	dialect  store.Dialect
}

// Dialect returns the database dialect for this test database.
func (tdb *TestDB) Dialect() store.Dialect { return tdb.dialect }

// NewForTesting prepares a fresh, isolated database on a TiDB instance. The
// returned TestDB is ready to use and the cleanup func drops the database and
// tears down the Docker container when one was started.
func NewForTesting(t *testing.T) (*TestDB, func()) {
	t.Helper()

	dialect := testDialect()
	dsn := os.Getenv("CHETTER_TEST_DSN")
	ownsContainer := false
	containerName := ""
	if dsn == "" {
		containerName = "chetter-test-db-" + randHex(6)
		port, err := freePort()
		if err != nil {
			t.Fatalf("find free port: %v", err)
		}
		startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		ownsContainer = startDBContainer(startCtx, containerName, port, dialect)
		if ownsContainer {
			dsn = testDSN(dialect, port)
		} else if local := os.Getenv(localFallbackEnv(dialect)); local != "" {
			dsn = "root@tcp(" + local + ")/?parseTime=true&multiStatements=true"
			if dialect == store.DialectMySQL {
				dsn = "root:root@tcp(" + local + ")/?parseTime=true&multiStatements=true"
			}
		} else {
			t.Skip("no CHETTER_TEST_DSN, Docker database failed to start, and no local fallback env var is set; skipping integration test")
			return nil, func() {}
		}
	}

	admin, err := sql.Open("mysql", dsn)
	if err != nil {
		cleanupContainer(ownsContainer, containerName)
		t.Fatalf("open admin db: %v", err)
	}
	configureTestDB(admin)

	waitForReady(t, admin)

	dbName := "chetter_test_" + randHex(6)
	if _, err := admin.Exec("CREATE DATABASE `" + dbName + "`"); err != nil {
		cleanupContainer(ownsContainer, containerName)
		t.Fatalf("create test database: %v", err)
	}

	testDSN := replaceDBName(dsn, dbName)
	db, err := sql.Open("mysql", testDSN)
	if err != nil {
		dropDatabase(admin, dbName)
		cleanupContainer(ownsContainer, containerName)
		t.Fatalf("open test db: %v", err)
	}
	configureTestDB(db)

	st, err := store.Open(testDSN, dialect)
	if err != nil {
		_ = db.Close()
		dropDatabase(admin, dbName)
		_ = admin.Close()
		cleanupContainer(ownsContainer, containerName)
		t.Fatalf("open store for schema: %v", err)
	}
	if err := st.ApplySchema(context.Background()); err != nil {
		_ = st.Close()
		_ = db.Close()
		dropDatabase(admin, dbName)
		_ = admin.Close()
		cleanupContainer(ownsContainer, containerName)
		t.Fatalf("apply schema: %v", err)
	}
	_ = st.Close()

	tdb := &TestDB{
		DB:       db,
		DSN:      testDSN,
		database: dbName,
		dialect:  dialect,
	}
	cleanup := func() {
		_ = db.Close()
		dropDatabase(admin, dbName)
		_ = admin.Close()
		cleanupContainer(ownsContainer, containerName)
	}
	return tdb, cleanup
}

// Truncate wipes all rows from chetter tables but keeps the schema.
func (tdb *TestDB) Truncate(t *testing.T) {
	t.Helper()
	for _, table := range []string{
		"chetter_trigger_runs",
		"chetter_triggers",
		"chetter_runners",
		"chetter_task_events",
		"chetter_tasks",
		"api_tokens",
		"users",
		"teams",
	} {
		if _, err := tdb.DB.Exec("DELETE FROM " + table); err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
}

// DatabaseName returns the per-run database name.
func (tdb *TestDB) DatabaseName() string { return tdb.database }

// OpenStore returns a *store.Store backed by this test database. Useful for
// tests that exercise the legacy store API alongside the new sqlc repository.
func (tdb *TestDB) OpenStore(t *testing.T) (*store.Store, func()) {
	t.Helper()
	st, err := store.Open(tdb.DSN, tdb.dialect)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return st, func() { _ = st.Close() }
}

func waitForReady(t testing.TB, db *sql.DB) {
	t.Helper()
	deadline := time.Now().Add(connectTimeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := db.PingContext(ctx)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("database not ready within timeout")
}

func cleanupContainer(ownsContainer bool, name string) {
	if !ownsContainer {
		return
	}
	_ = exec.Command("docker", "stop", name).Run()
}

var dockerCheckOnce sync.Once
var dockerCheckOK bool

func isDockerUnavailable(err error) bool {
	if !errors.Is(err, exec.ErrNotFound) && !strings.Contains(err.Error(), "executable file not found") {
		return false
	}
	dockerCheckOnce.Do(func() {
		_, lookErr := exec.LookPath("docker")
		dockerCheckOK = lookErr == nil
	})
	return !dockerCheckOK
}

func dropDatabase(db *sql.DB, name string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, _ = db.ExecContext(ctx, "DROP DATABASE IF EXISTS `"+name+"`")
}

func configureTestDB(db *sql.DB) {
	db.SetMaxOpenConns(testMaxOpenConns)
	db.SetMaxIdleConns(testMaxIdleConns)
	db.SetConnMaxLifetime(2 * time.Minute)
	db.SetConnMaxIdleTime(30 * time.Second)
}

func replaceDBName(dsn, dbName string) string {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		// Fall back to naive string replacement.
		if idx := strings.LastIndex(dsn, "/"); idx >= 0 {
			slash := strings.Index(dsn[idx+1:], "?")
			if slash < 0 {
				return dsn[:idx+1] + dbName
			}
			return dsn[:idx+1] + dbName + dsn[idx+1+slash:]
		}
		return dsn
	}
	cfg.DBName = dbName
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	if _, ok := cfg.Params["parseTime"]; !ok {
		cfg.Params["parseTime"] = "true"
	}
	if _, ok := cfg.Params["multiStatements"]; !ok {
		cfg.Params["multiStatements"] = "true"
	}
	return cfg.FormatDSN()
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// testDialect reads CHETTER_TEST_DB_DIALECT and returns the dialect to use
// for the test container. Defaults to TiDB for backward compatibility.
func testDialect() store.Dialect {
	d := store.ParseDialect(os.Getenv("CHETTER_TEST_DB_DIALECT"))
	if d == store.DialectUnknown {
		return store.DialectTiDB
	}
	return d
}

// startDBContainer starts a Docker container for the given dialect.
func startDBContainer(ctx context.Context, name string, port int, dialect store.Dialect) bool {
	image := os.Getenv("CHETTER_TEST_DB_IMAGE")
	if image == "" {
		if dialect == store.DialectMySQL {
			image = defaultMySQLImage
		} else {
			image = defaultTiDBImage
		}
	}
	var args []string
	if dialect == store.DialectMySQL {
		args = []string{
			"run", "--rm", "-d",
			"--name", name,
			"--ulimit", "nofile=65536:65536",
			"-p", fmt.Sprintf("%d:3306", port),
			"-e", "MYSQL_ROOT_PASSWORD=root",
			image,
			"--max-connections=500",
			"--table-open-cache=4096",
			"--innodb-buffer-pool-size=512M",
			"--innodb-log-buffer-size=64M",
			"--innodb-flush-log-at-trx-commit=2",
			"--sync-binlog=0",
			"--skip-log-bin",
			"--performance-schema=OFF",
			"--mysqlx=0",
		}
	} else {
		args = []string{
			"run", "--rm", "-d",
			"--name", name,
			"-p", fmt.Sprintf("%d:4000", port),
			"-p", fmt.Sprintf("%d:10080", port+1000),
			image,
			"--store=unistore", "--path=",
		}
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true
	}
	if isDockerUnavailable(err) {
		return false
	}
	fmt.Fprintf(os.Stderr, "docker run failed: %v\n%s\n", err, out)
	return false
}

// testDSN builds a DSN for the given dialect and port.
func testDSN(dialect store.Dialect, port int) string {
	if dialect == store.DialectMySQL {
		return fmt.Sprintf("root:root@tcp(127.0.0.1:%d)/?parseTime=true&multiStatements=true", port)
	}
	return fmt.Sprintf("root@tcp(127.0.0.1:%d)/?parseTime=true&multiStatements=true", port)
}

// localFallbackEnv returns the env var name for a local database fallback.
func localFallbackEnv(dialect store.Dialect) string {
	if dialect == store.DialectMySQL {
		return "CHETTER_TEST_LOCAL_MYSQL"
	}
	return "CHETTER_TEST_LOCAL_TIDB"
}

// PackageDB manages a single database Docker container shared across all tests
// in one package. Use StartPackageDB in TestMain for fast integration tests.
type PackageDB struct {
	containerName string
	ownsContainer bool
	adminDSN      string
	dialect       store.Dialect
}

// StartPackageDB starts a database container once per package. Call from TestMain.
// Pass *testing.M so cleanup runs after all tests complete via os.Exit.
func StartPackageDB(m *testing.M) *PackageDB {
	dialect := testDialect()
	dsn := os.Getenv("CHETTER_TEST_DSN")
	if dsn != "" {
		return &PackageDB{adminDSN: dsn, dialect: dialect}
	}
	if local := os.Getenv(localFallbackEnv(dialect)); local != "" {
		dsn = "root@tcp(" + local + ")/?parseTime=true&multiStatements=true"
		if dialect == store.DialectMySQL {
			dsn = "root:root@tcp(" + local + ")/?parseTime=true&multiStatements=true"
		}
		return &PackageDB{adminDSN: dsn, dialect: dialect}
	}
	containerName := "chetter-test-db-" + randHex(6)
	port, err := freePort()
	if err != nil {
		fmt.Fprintf(os.Stderr, "testdb: find free port: %v\n", err)
		return nil
	}
	startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if !startDBContainer(startCtx, containerName, port, dialect) {
		fmt.Fprintf(os.Stderr, "testdb: database container failed to start; skipping integration tests\n")
		return nil
	}
	dsn = testDSN(dialect, port)
	return &PackageDB{containerName: containerName, ownsContainer: true, adminDSN: dsn, dialect: dialect}
}

// Close shuts down the shared TiDB container.
func (p *PackageDB) Close() {
	if p.ownsContainer {
		_ = exec.Command("docker", "stop", p.containerName).Run()
	}
}

// AdminDB returns a connection to the database server for creating test databases.
func (p *PackageDB) AdminDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := sql.Open("mysql", p.adminDSN)
	if err != nil {
		t.Fatalf("testdb: open admin db: %v", err)
	}
	configureTestDB(db)
	waitForReady(t, db)
	return db
}

// NewTestDB creates a new isolated test database on the shared container,
// applies the schema, and returns a TestDB ready for test use.
func (p *PackageDB) NewTestDB(t testing.TB) (*TestDB, func()) {
	t.Helper()

	admin := p.AdminDB(t)

	dbName := "chetter_test_" + randHex(6)
	if _, err := admin.Exec("CREATE DATABASE `" + dbName + "`"); err != nil {
		_ = admin.Close()
		t.Fatalf("testdb: create test database: %v", err)
	}

	testDSN := replaceDBName(p.adminDSN, dbName)
	db, err := sql.Open("mysql", testDSN)
	if err != nil {
		dropDatabase(admin, dbName)
		_ = admin.Close()
		t.Fatalf("testdb: open test db: %v", err)
	}
	configureTestDB(db)

	st, err := store.Open(testDSN, p.dialect)
	if err != nil {
		_ = db.Close()
		dropDatabase(admin, dbName)
		_ = admin.Close()
		t.Fatalf("testdb: open store for schema: %v", err)
	}
	if err := st.ApplySchema(context.Background()); err != nil {
		_ = st.Close()
		_ = db.Close()
		dropDatabase(admin, dbName)
		_ = admin.Close()
		t.Fatalf("testdb: apply schema: %v", err)
	}
	_ = st.Close()

	cleanup := func() {
		_ = db.Close()
		dropDatabase(admin, dbName)
		_ = admin.Close()
	}

	return &TestDB{DB: db, DSN: testDSN, database: dbName, dialect: p.dialect}, cleanup
}
