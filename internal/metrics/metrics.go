// Package metrics exposes a Prometheus-compatible /metrics endpoint with
// standard Go runtime collectors and derived application gauges. See issue #92.
package metrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"time"

	"github.com/flatout-works/chetter/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Handler returns an http.Handler that serves Prometheus metrics.
// It registers standard Go process and runtime collectors plus a custom
// collector that queries the database for fleet health and webhook delivery
// status on every scrape.
func Handler(db *sql.DB, dialect store.Dialect) http.Handler {
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(newCollector(db, dialect))
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		ErrorLog: log.New(io.Discard, "", 0),
	})
}

// collector implements prometheus.Collector by querying the database for
// current fleet and webhook delivery state on each scrape. All gauges have
// bounded cardinality: labels are limited to status and slot-type enums.
type collector struct {
	db      *sql.DB
	dialect store.Dialect

	taskCount       *prometheus.Desc
	runnerCount     *prometheus.Desc
	runnerSlots     *prometheus.Desc
	relayRejections *prometheus.Desc
	webhookCount    *prometheus.Desc
	scrapeError     *prometheus.Desc
}

func newCollector(db *sql.DB, dialect store.Dialect) *collector {
	prefix := "chetter"
	return &collector{
		db:      db,
		dialect: dialect,
		taskCount: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "tasks"),
			"Number of tasks by status.",
			[]string{"status"}, nil,
		),
		runnerCount: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "runners"),
			"Number of runners by status (active or stale).",
			[]string{"status"}, nil,
		),
		runnerSlots: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "runner", "slots"),
			"Runner slots across the fleet.",
			[]string{"type"}, nil,
		),
		relayRejections: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "mcp_relay", "rejected_requests"),
			"Cumulative unauthorized MCP relay requests reported by runners.",
			nil, nil,
		),
		webhookCount: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "webhook_deliveries"),
			"Number of webhook deliveries by status.",
			[]string{"status"}, nil,
		),
		scrapeError: prometheus.NewDesc(
			prometheus.BuildFQName(prefix, "", "metrics_scrape_errors"),
			"Number of errors during metric collection.",
			nil, nil,
		),
	}
}

// Describe sends the metric descriptors to the channel.
func (c *collector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.taskCount
	ch <- c.runnerCount
	ch <- c.runnerSlots
	ch <- c.relayRejections
	ch <- c.webhookCount
	ch <- c.scrapeError
}

// Collect queries the database and emits current gauge values.
func (c *collector) Collect(ch chan<- prometheus.Metric) {
	if c.db == nil {
		// Emit zero-value metrics so the endpoint always publishes the
		// full set of HELP/TYPE lines for service discovery.
		c.emitZeroTaskMetrics(ch)
		c.emitZeroRunnerMetrics(ch)
		c.emitZeroWebhookMetrics(ch)
		ch <- prometheus.MustNewConstMetric(c.scrapeError, prometheus.GaugeValue, 1)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var errors float64

	if err := c.collectTasks(ctx, ch); err != nil {
		slog.Error("metrics: collect tasks", "err", err)
		c.emitZeroTaskMetrics(ch)
		errors++
	}
	if err := c.collectRunners(ctx, ch); err != nil {
		slog.Error("metrics: collect runners", "err", err)
		c.emitZeroRunnerMetrics(ch)
		errors++
	}
	if err := c.collectWebhookDeliveries(ctx, ch); err != nil {
		slog.Error("metrics: collect webhook deliveries", "err", err)
		c.emitZeroWebhookMetrics(ch)
		errors++
	}

	ch <- prometheus.MustNewConstMetric(c.scrapeError, prometheus.GaugeValue, errors)
}

func (c *collector) emitZeroTaskMetrics(ch chan<- prometheus.Metric) {
	for _, status := range []string{"pending", "running", "done", "error", "cancelled"} {
		ch <- prometheus.MustNewConstMetric(c.taskCount, prometheus.GaugeValue, 0, status)
	}
}

func (c *collector) emitZeroRunnerMetrics(ch chan<- prometheus.Metric) {
	for _, status := range []string{"active", "stale"} {
		ch <- prometheus.MustNewConstMetric(c.runnerCount, prometheus.GaugeValue, 0, status)
	}
	for _, slotType := range []string{"available", "occupied"} {
		ch <- prometheus.MustNewConstMetric(c.runnerSlots, prometheus.GaugeValue, 0, slotType)
	}
	ch <- prometheus.MustNewConstMetric(c.relayRejections, prometheus.GaugeValue, 0)
}

func (c *collector) emitZeroWebhookMetrics(ch chan<- prometheus.Metric) {
	for _, status := range []string{"received", "processing", "completed", "failed", "dead_letter"} {
		ch <- prometheus.MustNewConstMetric(c.webhookCount, prometheus.GaugeValue, 0, status)
	}
}

// collectTasks emits chetter_tasks gauges for each task status.
func (c *collector) collectTasks(ctx context.Context, ch chan<- prometheus.Metric) error {
	rows, err := c.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM tasks GROUP BY status`)
	if err != nil {
		return fmt.Errorf("query task status counts: %w", err)
	}
	defer rows.Close()

	counts := map[string]float64{
		"pending":   0,
		"running":   0,
		"done":      0,
		"error":     0,
		"cancelled": 0,
	}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return fmt.Errorf("scan task status count: %w", err)
		}
		counts[status] = float64(count)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows after task status counts: %w", err)
	}

	for status, count := range counts {
		ch <- prometheus.MustNewConstMetric(c.taskCount, prometheus.GaugeValue, count, status)
	}
	return nil
}

// collectRunners emits chetter_runners (active/stale) and
// chetter_runner_slots (available/occupied) gauges.
func (c *collector) collectRunners(ctx context.Context, ch chan<- prometheus.Metric) error {
	// maxRunnerPresenceSec mirrors the default used by GetRunnerFleetHealth.
	const maxRunnerPresenceSec = 120

	ageExpr := "TIMESTAMPDIFF(SECOND, last_seen_at, NOW())"
	if c.dialect == store.DialectPostgres {
		ageExpr = "FLOOR(EXTRACT(EPOCH FROM NOW() - last_seen_at))::int"
	}

	query := fmt.Sprintf(`
		SELECT %s AS last_seen_sec, max_concurrent, running_tasks, available_slots, metadata
		FROM runners
	`, ageExpr)
	rows, err := c.db.QueryContext(ctx, query)
	if err != nil {
		return fmt.Errorf("query runners: %w", err)
	}
	defer rows.Close()

	var active float64
	var stale float64
	var availableSlots float64
	var occupiedSlots float64
	var relayRejections float64

	for rows.Next() {
		var lastSeenSec int
		var maxConcurrent, runningTasks, availSlots int
		var metadata []byte
		if err := rows.Scan(&lastSeenSec, &maxConcurrent, &runningTasks, &availSlots, &metadata); err != nil {
			return fmt.Errorf("scan runner: %w", err)
		}
		relayRejections += float64(mcpRelayRejectedRequests(metadata))
		if lastSeenSec > maxRunnerPresenceSec {
			stale++
		} else {
			active++
			availableSlots += float64(max(availSlots, 0))
			occupiedSlots += float64(max(runningTasks, 0))
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows after runner query: %w", err)
	}

	ch <- prometheus.MustNewConstMetric(c.runnerCount, prometheus.GaugeValue, active, "active")
	ch <- prometheus.MustNewConstMetric(c.runnerCount, prometheus.GaugeValue, stale, "stale")
	ch <- prometheus.MustNewConstMetric(c.runnerSlots, prometheus.GaugeValue, availableSlots, "available")
	ch <- prometheus.MustNewConstMetric(c.runnerSlots, prometheus.GaugeValue, occupiedSlots, "occupied")
	ch <- prometheus.MustNewConstMetric(c.relayRejections, prometheus.GaugeValue, relayRejections)

	return nil
}

func mcpRelayRejectedRequests(metadata []byte) int64 {
	var heartbeat struct {
		RejectedRequests int64 `json:"mcp_relay_rejected_requests"`
	}
	if json.Unmarshal(metadata, &heartbeat) != nil || heartbeat.RejectedRequests < 0 {
		return 0
	}
	return heartbeat.RejectedRequests
}

// collectWebhookDeliveries emits chetter_webhook_deliveries gauges grouped
// by status (received, processing, completed, failed, dead_letter).
func (c *collector) collectWebhookDeliveries(ctx context.Context, ch chan<- prometheus.Metric) error {
	rows, err := c.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM webhook_deliveries GROUP BY status`)
	if err != nil {
		// The webhook_deliveries table may not exist if webhooks have never
		// been configured. Treat a missing table as zero deliveries of each
		// status rather than a scrape error.
		if isMissingTable(err) {
			for _, status := range []string{"received", "processing", "completed", "failed", "dead_letter"} {
				ch <- prometheus.MustNewConstMetric(c.webhookCount, prometheus.GaugeValue, 0, status)
			}
			return nil
		}
		return fmt.Errorf("query webhook delivery status counts: %w", err)
	}
	defer rows.Close()

	counts := map[string]float64{
		"received":    0,
		"processing":  0,
		"completed":   0,
		"failed":      0,
		"dead_letter": 0,
	}
	for rows.Next() {
		var status string
		var count int64
		if err := rows.Scan(&status, &count); err != nil {
			return fmt.Errorf("scan webhook delivery status count: %w", err)
		}
		counts[status] = float64(count)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("rows after webhook delivery status counts: %w", err)
	}

	for status, count := range counts {
		ch <- prometheus.MustNewConstMetric(c.webhookCount, prometheus.GaugeValue, count, status)
	}
	return nil
}

// isMissingTable returns true if the error indicates a missing table (MySQL
// error 1146 or PostgreSQL error 42P01).
func isMissingTable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// MySQL / TiDB: Error 1146 (42S02): Table doesn't exist
	// PostgreSQL: relation does not exist (SQLSTATE 42P01)
	return contains(msg, "1146") || contains(msg, "doesn't exist") ||
		contains(msg, "does not exist") || contains(msg, "42P01")
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
