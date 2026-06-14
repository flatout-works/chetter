// Package testdb spins up a real TiDB instance for integration tests.
//
// Tests use CHETTER_TEST_DSN to point at an existing TiDB (e.g. a CI service
// container). When that env var is unset, the package starts a one-shot TiDB
// Docker container for the test run and tears it down on exit.
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
	defaultTiDBImage = "pingcap/tidb:v8.5.1"
	connectTimeout   = 30 * time.Second
)

// TestDB owns a connection to a real TiDB instance plus the cleanup hooks
// the test should defer.
type TestDB struct {
	DB       *sql.DB
	DSN      string
	database string
}

// NewForTesting prepares a fresh, isolated database on a TiDB instance. The
// returned TestDB is ready to use and the cleanup func drops the database and
// tears down the Docker container when one was started.
func NewForTesting(t *testing.T) (*TestDB, func()) {
	t.Helper()

	dsn := os.Getenv("CHETTER_TEST_DSN")
	ownsContainer := false
	containerName := ""
	if dsn == "" {
		containerName = "chetter-test-tidb-" + randHex(6)
		port, err := freePort()
		if err != nil {
			t.Fatalf("find free port: %v", err)
		}
		image := os.Getenv("CHETTER_TEST_TIDB_IMAGE")
		if image == "" {
			image = defaultTiDBImage
		}
		startCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		ownsContainer = startTiDBContainer(startCtx, containerName, image, port)
		if ownsContainer {
			dsn = fmt.Sprintf("root@tcp(127.0.0.1:%d)/?parseTime=true&multiStatements=true", port)
		} else if local := os.Getenv("CHETTER_TEST_LOCAL_TIDB"); local != "" {
			dsn = "root@tcp(" + local + ")/?parseTime=true&multiStatements=true"
		} else {
			t.Skip("no CHETTER_TEST_DSN, CHETTER_TEST_TIDB_IMAGE-backed Docker TiDB failed to start, and CHETTER_TEST_LOCAL_TIDB is unset; skipping integration test")
			return nil, func() {}
		}
	}

	admin, err := sql.Open("mysql", dsn)
	if err != nil {
		cleanupContainer(ownsContainer, containerName)
		t.Fatalf("open admin db: %v", err)
	}

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

	st, err := store.Open(testDSN)
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
		"chetter_schedule_runs",
		"chetter_schedules",
		"chetter_runners",
		"chetter_task_events",
		"chetter_tasks",
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
	st, err := store.Open(tdb.DSN)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return st, func() { _ = st.Close() }
}

func waitForReady(t *testing.T, db *sql.DB) {
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
	t.Fatal("tidb not ready within timeout")
}

func startTiDBContainer(ctx context.Context, name, image string, port int) bool {
	args := []string{
		"run", "--rm", "-d",
		"--name", name,
		"-p", fmt.Sprintf("%d:4000", port),
		"-p", fmt.Sprintf("%d:10080", port+1000),
		image,
		"--store=unistore", "--path=",
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true
	}
	// If Docker is missing, fall back to the existing local TiDB on 4000.
	if isDockerUnavailable(err) {
		return false
	}
	fmt.Fprintf(os.Stderr, "docker run failed: %v\n%s\n", err, out)
	return false
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
	_, _ = db.Exec("DROP DATABASE IF EXISTS `" + name + "`")
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
