package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/testdb"
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

// enqueueCallbackDeliveriesOnlyForTest replays the outbox enqueue for an event
// whose task_events row has already been committed, without re-inserting that
// row. Used by the replay/idempotency assertions.
func enqueueCallbackDeliveriesOnlyForTest(t *testing.T, ctx context.Context, svc *Service, event TaskEventCallbackContext) {
	t.Helper()
	if err := withTxRetry(ctx, svc.rawDB, svc.dialect, func(q data.Repository) error {
		return svc.EnqueueTaskEventCallbackDeliveries(ctx, q, event)
	}); err != nil {
		t.Fatalf("enqueue callback delivery (replay): %v", err)
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
	// Only the delivery enqueue is replayed here: the task_events row is
	// already committed and InsertTaskEvent is a plain INSERT, so re-inserting
	// it would be a genuine duplicate-key error rather than the idempotency
	// under test (callback_deliveries' (callback_id, event_id) key).
	enqueueCallbackDeliveriesOnlyForTest(t, ctx, svc, event)
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

// createTaskCallbackForTest registers an enabled create_task callback with the
// given prompt and returns its name.
func createTaskCallbackForTest(t *testing.T, ctx context.Context, svc *Service, name, prompt string) {
	t.Helper()
	if _, err := svc.CreateEventCallback(ctx, EventCallbackInput{
		Name:         name,
		EventType:    "task.completed",
		ActionType:   EventCallbackActionCreateTask,
		ActionConfig: json.RawMessage(`{"prompt":` + strconv.Quote(prompt) + `}`),
		Enabled:      true,
	}); err != nil {
		t.Fatalf("create create_task callback: %v", err)
	}
}

// onlyCallbackDelivery returns the single callback delivery row in the test DB.
func onlyCallbackDelivery(t *testing.T, ctx context.Context, svc *Service) CallbackDeliveryRecord {
	t.Helper()
	rows, err := svc.ListCallbackDeliveries(ctx, "", 50, 0)
	if err != nil {
		t.Fatalf("list callback deliveries: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("callback deliveries = %d, want 1 (%+v)", len(rows), rows)
	}
	return rows[0]
}

func countTasksForTest(t *testing.T, tdb *testdb.TestDB) int {
	t.Helper()
	var total int
	if err := tdb.DB.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&total); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	return total
}

// TestCreateTaskCallbackDeliveryRecoveredAfterCrash is issue #405 AC-a: the
// create_task outbox row commits transactionally with its event and survives a
// replica crash before any worker ran. A later recovery cycle spawns the child
// exactly once, and a subsequent cycle (the delivery is now completed) does
// not spawn another.
func TestCreateTaskCallbackDeliveryRecoveredAfterCrash(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	// Callback context variables are server-owned rather than user input.
	svc.cfg.EnvValidation.BlockedPrefixes = nil

	source, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "source task", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit source task: %v", err)
	}
	createTaskCallbackForTest(t, ctx, svc, "spawn-child", "child of {{.TaskID}}")

	event := TaskEventCallbackContext{
		ID: "evt_crash", TaskID: source.ID, Subject: "connect.runner.r1." + source.ID,
		Status: "completed", EventType: "task.completed", Summary: "done",
		Payload: json.RawMessage(`{"status":"completed"}`), CreatedAt: time.Now().UTC(),
	}
	enqueueCallbackDeliveryForTest(t, ctx, svc, event)

	// Simulated crash: the outbox row is durable but no worker has executed it.
	if err := svc.processDueCallbackDeliveries(ctx); err != nil {
		t.Fatalf("recovery delivery cycle: %v", err)
	}
	if got := countTasksForTest(t, tdb); got != 2 {
		t.Fatalf("task count after recovery = %d, want 2 (source + one child)", got)
	}

	// A second cycle must not spawn a duplicate.
	if err := svc.processDueCallbackDeliveries(ctx); err != nil {
		t.Fatalf("second delivery cycle: %v", err)
	}
	if got := countTasksForTest(t, tdb); got != 2 {
		t.Fatalf("task count after second cycle = %d, want 2", got)
	}

	delivery := onlyCallbackDelivery(t, ctx, svc)
	if delivery.Status != callbackDeliveryStatusCompleted {
		t.Fatalf("delivery status = %q, want completed", delivery.Status)
	}
	if delivery.ActionType != EventCallbackActionCreateTask {
		t.Fatalf("delivery action_type = %q, want create_task", delivery.ActionType)
	}
	if delivery.ChildTaskID != callbackDeliveryTaskID(delivery.ID) {
		t.Fatalf("delivery child_task_id = %q, want deterministic %q", delivery.ChildTaskID, callbackDeliveryTaskID(delivery.ID))
	}
	if delivery.ChildTaskID == "" {
		t.Fatal("completed create_task delivery should record its child task id")
	}
}

// TestCreateTaskCallbackDeliveryReplayIsIdempotent is issue #405 AC-b: when a
// delivery is replayed after the child already spawned (crash between spawn and
// completion), the deterministic task id turns the duplicate-key error into a
// success and no second child appears.
func TestCreateTaskCallbackDeliveryReplayIsIdempotent(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	svc.cfg.EnvValidation.BlockedPrefixes = nil

	source, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "source task", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit source task: %v", err)
	}
	createTaskCallbackForTest(t, ctx, svc, "spawn-child", "child of {{.TaskID}}")

	event := TaskEventCallbackContext{
		ID: "evt_replay", TaskID: source.ID, Subject: "connect.runner.r1." + source.ID,
		Status: "completed", EventType: "task.completed", Summary: "done",
		Payload: json.RawMessage(`{"status":"completed"}`), CreatedAt: time.Now().UTC(),
	}
	enqueueCallbackDeliveryForTest(t, ctx, svc, event)

	delivery := onlyCallbackDelivery(t, ctx, svc)
	childTaskID := callbackDeliveryTaskID(delivery.ID)
	if err := svc.spawnCreateTaskFromEvent(ctx, event, "spawn-child", callbackCreateTaskConfig{Prompt: "child of {{.TaskID}}"}, childTaskID); err != nil {
		t.Fatalf("simulate pre-crash spawn: %v", err)
	}
	if got := countTasksForTest(t, tdb); got != 2 {
		t.Fatalf("task count after simulated spawn = %d, want 2", got)
	}

	// Replay: the worker must adopt the existing child instead of duplicating.
	if err := svc.processDueCallbackDeliveries(ctx); err != nil {
		t.Fatalf("replay delivery cycle: %v", err)
	}
	if got := countTasksForTest(t, tdb); got != 2 {
		t.Fatalf("task count after replay = %d, want 2 (no duplicate child)", got)
	}
	delivery = onlyCallbackDelivery(t, ctx, svc)
	if delivery.Status != callbackDeliveryStatusCompleted || delivery.ChildTaskID != childTaskID {
		t.Fatalf("replayed delivery = (status %q, child %q), want completed/%q", delivery.Status, delivery.ChildTaskID, childTaskID)
	}
}

// TestCreateTaskCallbackDeliveryPermanentFailureDeadLetters is issue #405
// AC-c: a create_task spawn that can never succeed (its source task no longer
// exists) dead-letters on the first attempt instead of retrying forever, and
// the dead letter is audited.
func TestCreateTaskCallbackDeliveryPermanentFailureDeadLetters(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	createTaskCallbackForTest(t, ctx, svc, "spawn-child", "child of {{.TaskID}}")
	event := TaskEventCallbackContext{
		ID: "evt_missing", TaskID: "task_missing", Subject: "connect.runner.r1.task_missing",
		Status: "completed", EventType: "task.completed", Summary: "done",
		Payload: json.RawMessage(`{"status":"completed"}`), CreatedAt: time.Now().UTC(),
	}
	enqueueCallbackDeliveryForTest(t, ctx, svc, event)

	if err := svc.processDueCallbackDeliveries(ctx); err != nil {
		t.Fatalf("delivery cycle: %v", err)
	}
	delivery := onlyCallbackDelivery(t, ctx, svc)
	if delivery.Status != callbackDeliveryStatusDeadLetter {
		t.Fatalf("delivery status = %q, want dead_letter", delivery.Status)
	}
	if delivery.Attempts != 1 {
		t.Fatalf("dead-letter attempts = %d, want 1 (terminal, not retried)", delivery.Attempts)
	}
	if delivery.Error == "" {
		t.Error("dead-letter delivery should carry an error")
	}
	if got := countTasksForTest(t, tdb); got != 0 {
		t.Fatalf("task count = %d, want 0", got)
	}

	var deadLetterAudits int
	if err := tdb.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE event_type = 'callback_delivery_dead_letter'").Scan(&deadLetterAudits); err != nil {
		t.Fatalf("count dead-letter audits: %v", err)
	}
	if deadLetterAudits != 1 {
		t.Fatalf("dead-letter audits = %d, want 1", deadLetterAudits)
	}
}

// TestCreateTaskCallbackDeliveryDepthLimitDeadLettersTerminally is issue #405
// AC-d: the issue #312 recursion-depth guard is re-checked at execution time;
// a rejected spawn dead-letters the delivery immediately, records the rejection
// on the parent task's event stream, and does not retry.
func TestCreateTaskCallbackDeliveryDepthLimitDeadLettersTerminally(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	svc.cfg.EnvValidation.BlockedPrefixes = nil
	svc.cfg.CallbackMaxDepth = 1

	source, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "source task", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit source task: %v", err)
	}
	if _, err := tdb.DB.Exec(testQuery(tdb.Dialect(),
		"UPDATE tasks SET callback_depth = 1 WHERE id = ?",
		"UPDATE tasks SET callback_depth = 1 WHERE id = $1"), source.ID); err != nil {
		t.Fatalf("set source callback depth: %v", err)
	}
	createTaskCallbackForTest(t, ctx, svc, "spawn-child", "child of {{.TaskID}}")

	event := TaskEventCallbackContext{
		ID: "evt_depth", TaskID: source.ID, Subject: "connect.runner.r1." + source.ID,
		Status: "completed", EventType: "task.completed", Summary: "done",
		Payload: json.RawMessage(`{"status":"completed"}`), CreatedAt: time.Now().UTC(),
	}
	enqueueCallbackDeliveryForTest(t, ctx, svc, event)

	if err := svc.processDueCallbackDeliveries(ctx); err != nil {
		t.Fatalf("delivery cycle: %v", err)
	}
	if got := countTasksForTest(t, tdb); got != 1 {
		t.Fatalf("task count = %d, want 1 (spawn rejected)", got)
	}
	delivery := onlyCallbackDelivery(t, ctx, svc)
	if delivery.Status != callbackDeliveryStatusDeadLetter {
		t.Fatalf("delivery status = %q, want dead_letter", delivery.Status)
	}
	if delivery.Attempts != 1 {
		t.Fatalf("depth-rejected delivery attempts = %d, want 1 (terminal, not retried)", delivery.Attempts)
	}
	if !strings.Contains(delivery.Error, eventCallbackRecursionError) {
		t.Fatalf("delivery error = %q, want it to mention %q", delivery.Error, eventCallbackRecursionError)
	}

	// The rejection is recorded on the parent task's event stream (issue #312
	// contract reused by the durable worker) and audited.
	var rejections int
	if err := tdb.DB.QueryRow(testQuery(tdb.Dialect(),
		"SELECT COUNT(*) FROM task_events WHERE task_id = ? AND event_type = ?",
		"SELECT COUNT(*) FROM task_events WHERE task_id = $1 AND event_type = $2"),
		source.ID, eventCallbackRejectedEvent).Scan(&rejections); err != nil {
		t.Fatalf("count rejection events: %v", err)
	}
	if rejections != 1 {
		t.Fatalf("rejection events = %d, want 1", rejections)
	}
	var recursionAudits int
	if err := tdb.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE event_type = 'event_callback_recursion_limit'").Scan(&recursionAudits); err != nil {
		t.Fatalf("count recursion audits: %v", err)
	}
	if recursionAudits != 1 {
		t.Fatalf("recursion-limit audits = %d, want 1", recursionAudits)
	}
}
