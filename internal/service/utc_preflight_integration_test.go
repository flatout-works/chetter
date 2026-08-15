package service

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/internal/store"
)

// TestStorePreflightForcesUTCSession verifies issue #316 against a real
// TiDB/MySQL database: the store preflight forces every connection to a UTC
// session time zone, and refuses a database (or explicit DSN) whose effective
// session time zone is not UTC.
func TestStorePreflightForcesUTCSession(t *testing.T) {
	if svcTestDB == nil {
		t.Skip("database unavailable; skipping integration test")
	}
	if svcTestDB.Dialect() == store.DialectPostgres {
		t.Skip("UTC session preflight targets TiDB/MySQL")
	}
	tdb, cleanup := svcTestDB.NewTestDB(t)
	defer cleanup()

	// Make the database genuinely non-UTC: a fresh session with session time
	// zone SYSTEM inherits the global +02:00 (the wowbagger failure mode).
	// Capture the prior global value so cleanup restores it exactly.
	var prevGlobal string
	if err := tdb.DB.QueryRow("SELECT @@global.time_zone").Scan(&prevGlobal); err != nil {
		t.Fatalf("capture global time zone: %v", err)
	}
	if _, err := tdb.DB.Exec("SET GLOBAL time_zone = '+02:00'"); err != nil {
		t.Fatalf("set global time zone: %v", err)
	}
	defer func() {
		if _, err := tdb.DB.Exec("SET GLOBAL time_zone = '" + prevGlobal + "'"); err != nil {
			t.Logf("restore global time zone %q: %v", prevGlobal, err)
		}
	}()

	ctx := context.Background()

	// A plain connection without the DSN time_zone fix inherits the non-UTC
	// global setting, proving the server really is non-UTC for unguarded
	// sessions.
	rawDB, err := sql.Open(store.DriverName(tdb.Dialect()), tdb.DSN)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	defer rawDB.Close()
	var rawSession, rawGlobal string
	if err := rawDB.QueryRowContext(ctx, "SELECT @@session.time_zone, @@global.time_zone").Scan(&rawSession, &rawGlobal); err != nil {
		t.Fatalf("query raw session time zone: %v", err)
	}
	if store.IsUTCTimeZone(rawSession, rawGlobal) {
		t.Fatalf("test setup expected a non-UTC session, got session=%q global=%q", rawSession, rawGlobal)
	}

	// store.Open injects time_zone='+00:00' into the DSN (normalizeDSN) and
	// the startup preflight must pass with the session forced to UTC.
	st, err := store.Open(tdb.DSN, tdb.Dialect())
	if err != nil {
		t.Fatalf("store.Open with a non-UTC database: %v", err)
	}
	defer st.Close()
	session, global, err := st.VerifyUTCSession(ctx)
	if err != nil {
		t.Fatalf("VerifyUTCSession after preflight: %v", err)
	}
	if !store.IsUTCTimeZone(session, global) {
		t.Fatalf("session time zone not UTC after preflight: session=%q global=%q", session, global)
	}
	if !strings.HasPrefix(session, "+00:00") && !strings.EqualFold(session, "UTC") {
		t.Fatalf("session time zone %q was not forced to UTC", session)
	}

	// An explicit non-UTC DSN time_zone parameter must be refused by the
	// preflight instead of silently skewing age math.
	separator := "?"
	if strings.Contains(tdb.DSN, "?") {
		separator = "&"
	}
	nonUTC := tdb.DSN + separator + "time_zone=" + url.QueryEscape("'+02:00'")
	bad, err := store.Open(nonUTC, tdb.Dialect())
	if err == nil {
		bad.Close()
		t.Fatal("store.Open accepted an explicit non-UTC DSN time_zone; want preflight refusal")
	}
	if !strings.Contains(err.Error(), "session time zone is not UTC") {
		t.Fatalf("store.Open error = %v; want a session-time-zone refusal", err)
	}
}
