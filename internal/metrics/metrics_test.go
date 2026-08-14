package metrics

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/flatout-works/chetter/internal/store"
)

func TestHandler_NonEmptyResponse(t *testing.T) {
	// Handler with nil DB still serves process and Go runtime metrics.
	h := Handler(nil, store.DialectTiDB)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	if body == "" {
		t.Fatal("expected non-empty response body")
	}
}

func TestHandler_ContentTypeText(t *testing.T) {
	h := Handler(nil, store.DialectTiDB)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/plain") {
		t.Fatalf("expected text/plain content type, got %q", ct)
	}
}

func TestHandler_StandardCollectors(t *testing.T) {
	h := Handler(nil, store.DialectTiDB)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)

	body := rec.Body.String()

	// Standard Go runtime metrics
	for _, name := range []string{
		"go_goroutines",
		"go_memstats_alloc_bytes",
		"process_open_fds",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("expected metric %q in output", name)
		}
	}
}

func TestHandler_CustomDescriptorsPresent(t *testing.T) {
	h := Handler(nil, store.DialectTiDB)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)

	body := rec.Body.String()

	// Custom chetter metrics should have HELP/TYPE lines
	for _, name := range []string{
		"chetter_tasks",
		"chetter_runners",
		"chetter_runner_slots",
		"chetter_mcp_relay_rejected_requests",
		"chetter_webhook_deliveries",
		"chetter_sessions",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("expected metric %q in output", name)
		}
	}
}

func TestCollector_Describe(t *testing.T) {
	c := newCollector(nil, store.DialectTiDB)
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
	if c.taskCount == nil || c.runnerCount == nil || c.runnerSlots == nil || c.relayRejections == nil || c.webhookCount == nil || c.sessionCount == nil {
		t.Fatal("expected all metric descriptors to be non-nil")
	}
}

func TestMCPRelayRejectedRequests(t *testing.T) {
	tests := []struct {
		metadata string
		want     int64
	}{
		{metadata: `{"mcp_relay_rejected_requests":7}`, want: 7},
		{metadata: `{}`, want: 0},
		{metadata: `{"mcp_relay_rejected_requests":-1}`, want: 0},
		{metadata: `{invalid`, want: 0},
	}
	for _, tt := range tests {
		if got := mcpRelayRejectedRequests([]byte(tt.metadata)); got != tt.want {
			t.Errorf("mcpRelayRejectedRequests(%q) = %d, want %d", tt.metadata, got, tt.want)
		}
	}
}

func TestIsMissingTable(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{fmt.Errorf("Error 1146: Table 'chetter.webhook_deliveries' doesn't exist"), true},
		{fmt.Errorf("relation \"webhook_deliveries\" does not exist"), true},
		{fmt.Errorf("pq: relation does not exist (SQLSTATE 42P01)"), true},
		{fmt.Errorf("connection refused"), false},
	}
	for _, tt := range tests {
		got := isMissingTable(tt.err)
		if got != tt.want {
			t.Errorf("isMissingTable(%q) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

// Test that Handler with a nil DB still produces valid metrics output.
func TestHandler_NoDatabase(t *testing.T) {
	h := Handler(nil, store.DialectTiDB)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()

	// When DB is nil, the custom collector returns scrape errors,
	// but the standard collectors still emit metrics.
	if !strings.Contains(body, "chetter_metrics_scrape_errors") {
		t.Error("expected scrape error metric when DB is nil")
	}
}

// Test that the Handler works with different dialects.
func TestHandler_Dialects(t *testing.T) {
	for _, dialect := range []store.Dialect{
		store.DialectTiDB,
		store.DialectMySQL,
		store.DialectPostgres,
	} {
		t.Run(dialect.String(), func(t *testing.T) {
			h := Handler(nil, dialect)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("expected 200 for dialect %s, got %d", dialect, rec.Code)
			}
		})
	}
}

// Integration test: test collector with a real database.
func TestCollector_WithDatabase_NoPanic(t *testing.T) {
	// This test ensures the SQL queries are valid by running them against
	// a real database. If no DB is available, skip.
	db := openTestDB(t)
	if db == nil {
		t.Skip("no test database available")
	}
	defer db.Close()

	// Create minimal schema so queries don't fail.
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS tasks (id VARCHAR(64), status VARCHAR(32))`,
		`CREATE TABLE IF NOT EXISTS runners (id VARCHAR(64), last_seen_at DATETIME(6), max_concurrent INT, running_tasks INT, available_slots INT, metadata JSON)`,
		`CREATE TABLE IF NOT EXISTS webhook_deliveries (id VARCHAR(64), status VARCHAR(32))`,
		`CREATE TABLE IF NOT EXISTS agent_sessions (id VARCHAR(64), status VARCHAR(32))`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("create test table: %v", err)
		}
	}

	_ = newCollector(db, store.DialectMySQL)

	h := Handler(db, store.DialectMySQL)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	// Should have task metrics with zero values for all statuses.
	for _, status := range []string{"pending", "running", "done", "error", "cancelled"} {
		needle := fmt.Sprintf(`chetter_tasks{status="%s"} 0`, status)
		if !strings.Contains(body, needle) {
			t.Errorf("expected %q in output", needle)
		}
	}

	// Should have runner metrics.
	for _, needle := range []string{
		`chetter_runners{status="active"} 0`,
		`chetter_runners{status="stale"} 0`,
		`chetter_runner_slots{type="available"} 0`,
		`chetter_runner_slots{type="occupied"} 0`,
		`chetter_mcp_relay_rejected_requests 0`,
	} {
		if !strings.Contains(body, needle) {
			t.Errorf("expected %q in output", needle)
		}
	}

	// Should have webhook delivery metrics.
	for _, status := range []string{"received", "processing", "completed", "failed", "dead_letter"} {
		needle := fmt.Sprintf(`chetter_webhook_deliveries{status="%s"} 0`, status)
		if !strings.Contains(body, needle) {
			t.Errorf("expected %q in output", needle)
		}
	}

	// Should have session metrics for every known status.
	for _, status := range sessionStatuses {
		needle := fmt.Sprintf(`chetter_sessions{status="%s"} 0`, status)
		if !strings.Contains(body, needle) {
			t.Errorf("expected %q in output", needle)
		}
	}

	// Scrape errors should be 0 when DB is available.
	if !strings.Contains(body, "chetter_metrics_scrape_errors 0") {
		t.Error("expected zero scrape errors with available DB")
	}
}

// openTestDB returns a test database connection or nil if none is available.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := "root@tcp(127.0.0.1:4000)/?parseTime=true"
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil
	}
	// Create a test database.
	if _, err := db.Exec("CREATE DATABASE IF NOT EXISTS chetter_metrics_test"); err != nil {
		db.Close()
		return nil
	}
	db.Close()

	db, err = sql.Open("mysql", "root@tcp(127.0.0.1:4000)/chetter_metrics_test?parseTime=true")
	if err != nil {
		return nil
	}
	return db
}
