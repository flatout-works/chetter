package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/repository"
)

// testCallbackDeliveryRow builds an in-memory delivery row for the low-level
// sender tests (no database involved).
func testCallbackDeliveryRow(endpointURL string) repository.CallbackDelivery {
	return repository.CallbackDelivery{
		ID:          "cbd_unit",
		CallbackID:  "ecb_unit",
		EventID:     "evt_unit",
		EventType:   "task.completed",
		EndpointUrl: endpointURL,
		Method:      http.MethodPost,
		Payload:     `{"status":"completed"}`,
		Status:      callbackDeliveryStatusInFlight,
		Attempts:    0,
		MaxAttempts: defaultCallbackDeliveryMaxAttempts,
	}
}

// enqueueCallbackDeliveryForTest mirrors the production outbox pattern: it
// inserts a task_events row and enqueues matching webhook/slack callback
// deliveries in one transaction (issue #357 AC1).
func enqueueCallbackDeliveryForTest(t *testing.T, ctx context.Context, svc *Service, event TaskEventCallbackContext) {
	t.Helper()
	if err := withTxRetry(ctx, svc.rawDB, svc.dialect, func(q data.Repository) error {
		if err := q.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
			ID:        event.ID,
			TaskID:    event.TaskID,
			Subject:   event.Subject,
			Status:    event.Status,
			EventType: event.EventType,
			Payload:   event.Payload,
			CreatedAt: event.CreatedAt,
		}); err != nil {
			return err
		}
		return svc.EnqueueTaskEventCallbackDeliveries(ctx, q, event)
	}); err != nil {
		t.Fatalf("enqueue callback delivery: %v", err)
	}
}

func TestCallbackDeliverySendUsesStoredPayload(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	svc.cfg.WebhookAllowHTTP = true
	svc.cfg.WebhookAllowPrivate = true

	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	delivery := testCallbackDeliveryRow(server.URL)
	if err := svc.sendCallbackDelivery(ctx, delivery); err != nil {
		t.Fatalf("sendCallbackDelivery: %v", err)
	}
	select {
	case body := <-received:
		if body != `{"status":"completed"}` {
			t.Errorf("received body %q, want prepared payload", body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("receiver never got the delivery")
	}
}

// TestCallbackDeliveryRetriesThenDeadLetters is the issue #357 AC6 integration
// test: a webhook callback whose endpoint keeps failing is retried with
// backoff and eventually dead-letters (attempts == max_attempts), with audit
// events recorded for the retries and the dead-letter transition.
func TestCallbackDeliveryRetriesThenDeadLetters(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	svc.cfg.WebhookAllowHTTP = true
	svc.cfg.WebhookAllowPrivate = true
	// Fast, deterministic backoff so the test does not wait out the default
	// exponential schedule between worker cycles.
	svc.deliveryBackoff = func(attempts int32) time.Duration { return 20 * time.Millisecond }

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	if _, err := svc.CreateEventCallback(ctx, EventCallbackInput{
		Name:         "failing-hook",
		EventType:    "task.completed",
		ActionType:   EventCallbackActionWebhook,
		ActionConfig: json.RawMessage(`{"url":"` + server.URL + `/hook","method":"POST"}`),
		Enabled:      true,
	}); err != nil {
		t.Fatalf("create failing webhook callback: %v", err)
	}

	now := time.Now().UTC()
	event := TaskEventCallbackContext{
		ID: "evt_retry", TaskID: "task_retry", Subject: "connect.runner.r1.task_retry",
		Status: "completed", EventType: "task.completed", Summary: "done",
		Payload:   json.RawMessage(`{"status":"completed"}`),
		CreatedAt: now,
	}
	enqueueCallbackDeliveryForTest(t, ctx, svc, event)

	// Drive worker cycles until the delivery exhausts its attempts. The 20ms
	// test backoff is shorter than the sleep between cycles, so every cycle
	// reclaims the failed delivery until it dead-letters.
	for i := 0; i < 8; i++ {
		if err := svc.processDueCallbackDeliveries(ctx); err != nil {
			t.Fatalf("delivery cycle %d: %v", i, err)
		}
		time.Sleep(30 * time.Millisecond)
	}

	if got := hits.Load(); got != 3 {
		t.Fatalf("HTTP delivery attempts = %d, want 3 (initial + 2 retries, then dead letter)", got)
	}
	rows, err := svc.ListCallbackDeliveries(ctx, callbackDeliveryStatusDeadLetter, 50, 0)
	if err != nil {
		t.Fatalf("list dead-letter deliveries: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("dead-letter deliveries = %d, want 1", len(rows))
	}
	if rows[0].EventID != event.ID || rows[0].TaskID != event.TaskID {
		t.Errorf("delivery provenance = (event %s, task %s), want (%s, %s)", rows[0].EventID, rows[0].TaskID, event.ID, event.TaskID)
	}
	if rows[0].Attempts != 3 || rows[0].MaxAttempts != 3 {
		t.Errorf("dead-letter attempts = %d/%d, want 3/3", rows[0].Attempts, rows[0].MaxAttempts)
	}
	if rows[0].Error == "" {
		t.Error("dead-letter delivery should carry an error")
	}

	// Retries and the dead-letter transition are audited (AC4).
	var failedAudits, deadLetterAudits int
	if err := tdb.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE event_type = 'callback_delivery_failed'`).Scan(&failedAudits); err != nil {
		t.Fatalf("count failed audits: %v", err)
	}
	if err := tdb.DB.QueryRow(`SELECT COUNT(*) FROM audit_log WHERE event_type = 'callback_delivery_dead_letter'`).Scan(&deadLetterAudits); err != nil {
		t.Fatalf("count dead-letter audits: %v", err)
	}
	if failedAudits != 2 || deadLetterAudits != 1 {
		t.Errorf("audit events = (%d failed, %d dead_letter), want (2, 1)", failedAudits, deadLetterAudits)
	}
}

// TestCallbackDeliveryLaterSuccessMarksCompleted is the second half of the
// issue #357 AC6 scenario: once the endpoint recovers, a new delivery for the
// same callback completes instead of retrying forever.
func TestCallbackDeliveryLaterSuccessMarksCompleted(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	svc.cfg.WebhookAllowHTTP = true
	svc.cfg.WebhookAllowPrivate = true

	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- string(body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if _, err := svc.CreateEventCallback(ctx, EventCallbackInput{
		Name:         "recovered-hook",
		EventType:    "task.failed",
		ActionType:   EventCallbackActionWebhook,
		ActionConfig: json.RawMessage(`{"url":"` + server.URL + `/hook","method":"POST","headers":{"X-Source":"test"}}`),
		Enabled:      true,
	}); err != nil {
		t.Fatalf("create recovered webhook callback: %v", err)
	}

	event := TaskEventCallbackContext{
		ID: "evt_success", TaskID: "task_success", Subject: "connect.runner.r1.task_success",
		Status: "failed", EventType: "task.failed", Summary: "boom",
		Payload:   json.RawMessage(`{"status":"failed"}`),
		CreatedAt: time.Now().UTC(),
	}
	enqueueCallbackDeliveryForTest(t, ctx, svc, event)

	if err := svc.processDueCallbackDeliveries(ctx); err != nil {
		t.Fatalf("delivery cycle: %v", err)
	}
	select {
	case body := <-received:
		if body == "" {
			t.Error("expected a rendered JSON payload")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("receiver never got the delivery")
	}

	rows, err := svc.ListCallbackDeliveries(ctx, callbackDeliveryStatusCompleted, 50, 0)
	if err != nil {
		t.Fatalf("list completed deliveries: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("completed deliveries = %d, want 1", len(rows))
	}
	if rows[0].EventID != event.ID {
		t.Errorf("completed delivery event = %q, want %q", rows[0].EventID, event.ID)
	}
	if rows[0].Error != "" || rows[0].ProcessedAt == nil {
		t.Errorf("completed delivery should have no error and a processed_at, got error %q processed %v", rows[0].Error, rows[0].ProcessedAt)
	}
}

// TestCallbackDeliveryEnqueueTransactionalWithTaskEvent verifies the durable
// outbox property (issue #357 AC1): the delivery row commits atomically with
// its task_events row (a rolled-back event leaves no orphan delivery), and a
// replay of the same event cannot double-enqueue thanks to the unique
// (callback_id, event_id) key.
func TestCallbackDeliveryEnqueueTransactionalWithTaskEvent(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	svc.cfg.WebhookAllowHTTP = true
	svc.cfg.WebhookAllowPrivate = true

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if _, err := svc.CreateEventCallback(ctx, EventCallbackInput{
		Name:         "tx-hook",
		EventType:    "task.progress",
		ActionType:   EventCallbackActionWebhook,
		ActionConfig: json.RawMessage(`{"url":"` + server.URL + `/hook"}`),
		Enabled:      true,
	}); err != nil {
		t.Fatalf("create tx webhook callback: %v", err)
	}

	now := time.Now().UTC()
	event := TaskEventCallbackContext{
		ID: "evt_tx_commit", TaskID: "task_tx", Subject: "connect.runner.r1.task_tx",
		Status: "running", EventType: "task.progress", Summary: "progress",
		Payload:   json.RawMessage(`{"progress":1}`),
		CreatedAt: now,
	}

	// Commit path: event row and delivery row appear together.
	enqueueCallbackDeliveryForTest(t, ctx, svc, event)
	var eventRows int
	if err := tdb.DB.QueryRow(testQuery(tdb.Dialect(),
		`SELECT COUNT(*) FROM task_events WHERE id = ?`,
		`SELECT COUNT(*) FROM task_events WHERE id = $1`), event.ID).Scan(&eventRows); err != nil {
		t.Fatalf("count task events: %v", err)
	}
	if eventRows != 1 {
		t.Fatalf("task_events rows = %d, want 1", eventRows)
	}
	rows, err := svc.ListCallbackDeliveries(ctx, "", 50, 0)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(rows) != 1 || rows[0].EventID != event.ID || rows[0].Status != callbackDeliveryStatusPending {
		t.Fatalf("deliveries after commit = %+v, want 1 pending row for %s", rows, event.ID)
	}

	// Replay path: the same event enqueued again is a no-op (unique key).
	enqueueCallbackDeliveryForTest(t, ctx, svc, event)
	rows, err = svc.ListCallbackDeliveries(ctx, "", 50, 0)
	if err != nil {
		t.Fatalf("list deliveries after replay: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("deliveries after replay = %d, want 1 (idempotent enqueue)", len(rows))
	}

	// Rollback path: when the enclosing transaction rolls back, neither the
	// event row nor its delivery row survives.
	rollbackEvent := TaskEventCallbackContext{
		ID: "evt_tx_rollback", TaskID: "task_tx", Subject: "connect.runner.r1.task_tx",
		Status: "running", EventType: "task.progress", Summary: "progress",
		Payload:   json.RawMessage(`{"progress":2}`),
		CreatedAt: now,
	}
	err = withTxRetry(ctx, svc.rawDB, svc.dialect, func(q data.Repository) error {
		if err := q.InsertTaskEvent(ctx, repository.InsertTaskEventParams{
			ID: rollbackEvent.ID, TaskID: rollbackEvent.TaskID, Subject: rollbackEvent.Subject,
			Status: rollbackEvent.Status, EventType: rollbackEvent.EventType,
			Payload: rollbackEvent.Payload, CreatedAt: rollbackEvent.CreatedAt,
		}); err != nil {
			return err
		}
		if err := svc.EnqueueTaskEventCallbackDeliveries(ctx, q, rollbackEvent); err != nil {
			return err
		}
		return errors.New("simulated post-enqueue failure")
	})
	if err == nil {
		t.Fatal("expected the simulated transaction failure")
	}
	if err := tdb.DB.QueryRow(testQuery(tdb.Dialect(),
		`SELECT COUNT(*) FROM task_events WHERE id = ?`,
		`SELECT COUNT(*) FROM task_events WHERE id = $1`), rollbackEvent.ID).Scan(&eventRows); err != nil {
		t.Fatalf("count rolled-back task events: %v", err)
	}
	if eventRows != 0 {
		t.Fatalf("rolled-back task_events rows = %d, want 0", eventRows)
	}
	rows, err = svc.ListCallbackDeliveries(ctx, "", 50, 0)
	if err != nil {
		t.Fatalf("list deliveries after rollback: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("deliveries after rollback = %d, want 1 (only the committed event's)", len(rows))
	}
}

// TestCallbackDeliveryListStatusFilter verifies the admin list tool filter.
func TestCallbackDeliveryListStatusFilter(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	svc.cfg.WebhookAllowHTTP = true
	svc.cfg.WebhookAllowPrivate = true

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if _, err := svc.CreateEventCallback(ctx, EventCallbackInput{
		Name:         "filter-hook",
		EventType:    "task.completed",
		ActionType:   EventCallbackActionWebhook,
		ActionConfig: json.RawMessage(`{"url":"` + server.URL + `/hook"}`),
		Enabled:      true,
	}); err != nil {
		t.Fatalf("create filter webhook callback: %v", err)
	}

	now := time.Now().UTC()
	enqueueCallbackDeliveryForTest(t, ctx, svc, TaskEventCallbackContext{
		ID: "evt_filter", TaskID: "task_filter", Subject: "s", Status: "completed",
		EventType: "task.completed", Summary: "done", Payload: json.RawMessage(`{}`), CreatedAt: now,
	})

	pending, err := svc.ListCallbackDeliveries(ctx, callbackDeliveryStatusPending, 50, 0)
	if err != nil {
		t.Fatalf("list pending deliveries: %v", err)
	}
	if len(pending) != 1 || pending[0].Status != callbackDeliveryStatusPending {
		t.Fatalf("pending deliveries = %+v, want 1 pending", pending)
	}
	completed, err := svc.ListCallbackDeliveries(ctx, callbackDeliveryStatusCompleted, 50, 0)
	if err != nil {
		t.Fatalf("list completed deliveries: %v", err)
	}
	if len(completed) != 0 {
		t.Fatalf("completed deliveries = %d, want 0", len(completed))
	}

	// The delivery row is fully processed by one worker cycle.
	if err := svc.processDueCallbackDeliveries(ctx); err != nil {
		t.Fatalf("delivery cycle: %v", err)
	}
	pending, err = svc.ListCallbackDeliveries(ctx, callbackDeliveryStatusPending, 50, 0)
	if err != nil {
		t.Fatalf("list pending deliveries after cycle: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending deliveries after cycle = %d, want 0", len(pending))
	}
	completed, err = svc.ListCallbackDeliveries(ctx, callbackDeliveryStatusCompleted, 50, 0)
	if err != nil {
		t.Fatalf("list completed deliveries after cycle: %v", err)
	}
	if len(completed) != 1 {
		t.Fatalf("completed deliveries after cycle = %d, want 1", len(completed))
	}
}
