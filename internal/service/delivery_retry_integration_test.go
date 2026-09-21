package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRetryCallbackDeliveryRedeliversDeadLetter is the issue #421 happy path
// for outbound callback actions: a dead_letter delivery is reset to a due
// pending row by an operator and the existing leased worker redelivers it to
// the (now recovered) destination exactly once.
func TestRetryCallbackDeliveryRedeliversDeadLetter(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	svc.cfg.WebhookAllowHTTP = true
	svc.cfg.WebhookAllowPrivate = true
	svc.deliveryBackoff = func(attempts int32) time.Duration { return 20 * time.Millisecond }

	var fail atomic.Bool
	fail.Store(true)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if _, err := svc.CreateEventCallback(ctx, EventCallbackInput{
		Name:         "recoverable-hook",
		EventType:    "task.completed",
		ActionType:   EventCallbackActionWebhook,
		ActionConfig: json.RawMessage(`{"url":"` + server.URL + `/hook","method":"POST"}`),
		Enabled:      true,
	}); err != nil {
		t.Fatalf("create webhook callback: %v", err)
	}

	enqueueCallbackDeliveryForTest(t, ctx, svc, TaskEventCallbackContext{
		ID: "evt_redeliver", TaskID: "task_redeliver", Subject: "connect.runner.r1.task_redeliver",
		Status: "completed", EventType: "task.completed", Summary: "done",
		Payload: json.RawMessage(`{"status":"completed"}`), CreatedAt: time.Now().UTC(),
	})

	// Exhaust the retry budget so the delivery dead-letters.
	for i := 0; i < 8; i++ {
		if err := svc.processDueCallbackDeliveries(ctx); err != nil {
			t.Fatalf("delivery cycle %d: %v", i, err)
		}
		time.Sleep(30 * time.Millisecond)
	}
	delivery := onlyCallbackDelivery(t, ctx, svc)
	if delivery.Status != callbackDeliveryStatusDeadLetter {
		t.Fatalf("delivery status = %q, want dead_letter before retry", delivery.Status)
	}
	hitsBeforeRetry := hits.Load()

	// The destination recovers; the operator retries the dead letter.
	fail.Store(false)
	retried, err := svc.RetryCallbackDelivery(ctxWithAdmin(ctx), delivery.ID)
	if err != nil {
		t.Fatalf("RetryCallbackDelivery: %v", err)
	}
	if retried.ID != delivery.ID || retried.Status != callbackDeliveryStatusPending {
		t.Fatalf("retried delivery = (id %q status %q), want same id in pending", retried.ID, retried.Status)
	}
	if retried.Attempts != 0 {
		t.Fatalf("retried delivery attempts = %d, want 0", retried.Attempts)
	}
	if retried.NextAttemptAt == nil {
		t.Fatal("retried delivery should have a next_attempt_at so the worker picks it up")
	}
	if retried.Error != "" {
		t.Fatalf("retried delivery error = %q, want cleared", retried.Error)
	}

	// The worker redelivers the reset row and it succeeds.
	if err := svc.processDueCallbackDeliveries(ctx); err != nil {
		t.Fatalf("delivery cycle after retry: %v", err)
	}
	if got := hits.Load(); got != hitsBeforeRetry+1 {
		t.Fatalf("destination hits after retry = %d, want %d (one redelivery attempt)", got, hitsBeforeRetry+1)
	}
	delivery = onlyCallbackDelivery(t, ctx, svc)
	if delivery.Status != callbackDeliveryStatusCompleted {
		t.Fatalf("delivery status after retry = %q, want completed", delivery.Status)
	}

	var retriedAudits int
	if err := tdb.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE event_type = 'callback_delivery_retried'").Scan(&retriedAudits); err != nil {
		t.Fatalf("count retry audits: %v", err)
	}
	if retriedAudits != 1 {
		t.Fatalf("callback_delivery_retried audits = %d, want 1", retriedAudits)
	}
}

// TestRetryCallbackDeliveryGuards verifies the issue #421 guards: completed
// rows and rows with a live in_flight lease are rejected with no state change,
// and non-admin callers are denied.
func TestRetryCallbackDeliveryGuards(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	svc.cfg.WebhookAllowHTTP = true
	svc.cfg.WebhookAllowPrivate = true

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if _, err := svc.CreateEventCallback(ctx, EventCallbackInput{
		Name:         "guard-hook",
		EventType:    "task.completed",
		ActionType:   EventCallbackActionWebhook,
		ActionConfig: json.RawMessage(`{"url":"` + server.URL + `/hook","method":"POST"}`),
		Enabled:      true,
	}); err != nil {
		t.Fatalf("create webhook callback: %v", err)
	}
	enqueueCallbackDeliveryForTest(t, ctx, svc, TaskEventCallbackContext{
		ID: "evt_guard", TaskID: "task_guard", Subject: "connect.runner.r1.task_guard",
		Status: "completed", EventType: "task.completed", Summary: "done",
		Payload: json.RawMessage(`{"status":"completed"}`), CreatedAt: time.Now().UTC(),
	})

	// Complete the delivery, then reject a retry from the completed state.
	if err := svc.processDueCallbackDeliveries(ctx); err != nil {
		t.Fatalf("delivery cycle: %v", err)
	}
	completed := onlyCallbackDelivery(t, ctx, svc)
	if completed.Status != callbackDeliveryStatusCompleted {
		t.Fatalf("delivery status = %q, want completed", completed.Status)
	}
	if _, err := svc.RetryCallbackDelivery(ctxWithAdmin(ctx), completed.ID); err == nil {
		t.Fatal("expected retry of a completed delivery to be rejected")
	} else if !strings.Contains(err.Error(), "cannot be retried from status") {
		t.Fatalf("completed retry error = %q, want a status explanation", err)
	}

	// Non-admin callers are denied.
	if _, err := svc.RetryCallbackDelivery(ctxWithTeam(ctx, "team_x"), completed.ID); err == nil {
		t.Fatal("expected team-scoped callback retry to be denied")
	} else if err.Error() != "admin access required" {
		t.Fatalf("team-scoped retry error = %q, want admin access required", err)
	}

	// Simulate a live in_flight lease: the guarded reset must not touch it.
	future := time.Now().UTC().Add(time.Hour)
	if _, err := svc.rawDB.ExecContext(ctx, testQuery(svc.dialect,
		`UPDATE callback_deliveries SET status = 'in_flight', attempts = 1, lease_expires_at = ?, updated_at = ? WHERE id = ?`,
		`UPDATE callback_deliveries SET status = 'in_flight', attempts = 1, lease_expires_at = $1, updated_at = $2 WHERE id = $3`),
		future, time.Now().UTC(), completed.ID); err != nil {
		t.Fatalf("force in_flight state: %v", err)
	}
	if _, err := svc.RetryCallbackDelivery(ctxWithAdmin(ctx), completed.ID); err == nil {
		t.Fatal("expected retry of a live in_flight delivery to be rejected")
	} else if !strings.Contains(err.Error(), "cannot be retried from status") {
		t.Fatalf("in_flight retry error = %q, want a status explanation", err)
	}
	still := onlyCallbackDelivery(t, ctx, svc)
	if still.Status != callbackDeliveryStatusInFlight || still.Attempts != 1 {
		t.Fatalf("rejected retry mutated the row: status %q attempts %d", still.Status, still.Attempts)
	}

	if hits.Load() != 1 {
		t.Fatalf("destination hits = %d, want 1 (no extra redelivery from rejected retries)", hits.Load())
	}
}

// TestRetryCallbackDeliveryCreateTaskNoDuplicate is the issue #421
// duplicate-suppression guarantee for create_task automations: a delivery that
// already spawned its child (crash between spawn and completion) can be reset
// and reprocessed without creating a second task, because the child id is
// derived deterministically from the delivery row.
func TestRetryCallbackDeliveryCreateTaskNoDuplicate(t *testing.T) {
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
		ID: "evt_retry_child", TaskID: source.ID, Subject: "connect.runner.r1." + source.ID,
		Status: "completed", EventType: "task.completed", Summary: "done",
		Payload: json.RawMessage(`{"status":"completed"}`), CreatedAt: time.Now().UTC(),
	}
	enqueueCallbackDeliveryForTest(t, ctx, svc, event)

	// Spawn the child directly (pre-crash) but leave the delivery eligible for
	// retry, as if the worker crashed before recording completion.
	delivery := onlyCallbackDelivery(t, ctx, svc)
	childTaskID := callbackDeliveryTaskID(delivery.ID)
	if err := svc.spawnCreateTaskFromEvent(ctx, event, "spawn-child", callbackCreateTaskConfig{Prompt: "child of {{.TaskID}}"}, childTaskID); err != nil {
		t.Fatalf("simulate pre-crash spawn: %v", err)
	}
	if _, err := svc.rawDB.ExecContext(ctx, testQuery(svc.dialect,
		`UPDATE callback_deliveries SET status = 'failed', attempts = 1, error = 'simulated crash', updated_at = ? WHERE id = ?`,
		`UPDATE callback_deliveries SET status = 'failed', attempts = 1, error = 'simulated crash', updated_at = $1 WHERE id = $2`),
		time.Now().UTC(), delivery.ID); err != nil {
		t.Fatalf("force failed state: %v", err)
	}

	if _, err := svc.RetryCallbackDelivery(ctxWithAdmin(ctx), delivery.ID); err != nil {
		t.Fatalf("RetryCallbackDelivery: %v", err)
	}
	if err := svc.processDueCallbackDeliveries(ctx); err != nil {
		t.Fatalf("delivery cycle after retry: %v", err)
	}
	if got := countTasksForTest(t, tdb); got != 2 {
		t.Fatalf("task count after retry = %d, want 2 (source + exactly one child)", got)
	}
	delivery = onlyCallbackDelivery(t, ctx, svc)
	if delivery.Status != callbackDeliveryStatusCompleted || delivery.ChildTaskID != childTaskID {
		t.Fatalf("retried delivery = (status %q child %q), want completed/%q", delivery.Status, delivery.ChildTaskID, childTaskID)
	}
}

// TestRetryInboundDeliveryRedeliversDeadLetter is the issue #421 happy path
// for inbound deliveries: a failed_permanent delivery is reset to pending and
// the worker turns it into exactly one task.
func TestRetryInboundDeliveryRedeliversDeadLetter(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	seedTestInboundEndpoint(t, ctx, svc, "inb_retry", "pub_retry", "global/webhooks/inbound/retry.yaml",
		`Investigate {{ .EventType }} {{ .Payload.repository }}`, `["build.completed"]`)
	seedTestInboundDelivery(t, ctx, svc, "ibd_retry", "inb_retry", "build.completed", []byte(`{"repository":"acme/web"}`))

	// Force the terminal state an operator would find and retry.
	now := time.Now().UTC()
	if _, err := svc.rawDB.ExecContext(ctx, testQuery(svc.dialect,
		`UPDATE inbound_deliveries SET status = 'dead_letter', attempts = 3, error = 'destination down', updated_at = ? WHERE id = ?`,
		`UPDATE inbound_deliveries SET status = 'dead_letter', attempts = 3, error = 'destination down', updated_at = $1 WHERE id = $2`),
		now, "ibd_retry"); err != nil {
		t.Fatalf("force dead_letter state: %v", err)
	}

	retried, err := svc.RetryInboundDelivery(ctxWithAdmin(ctx), "ibd_retry")
	if err != nil {
		t.Fatalf("RetryInboundDelivery: %v", err)
	}
	if retried.ID != "ibd_retry" || retried.Status != inboundDeliveryStatusPending {
		t.Fatalf("retried inbound delivery = (id %q status %q), want pending", retried.ID, retried.Status)
	}
	if retried.Attempts != 0 || retried.Error != "" {
		t.Fatalf("retried inbound delivery = (attempts %d error %q), want 0 and empty", retried.Attempts, retried.Error)
	}

	if err := svc.processDueInboundDeliveries(ctx); err != nil {
		t.Fatalf("process after retry: %v", err)
	}
	deliveries, err := svc.ListInboundDeliveries(ctx, "", "", 10, 0)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != inboundDeliveryStatusSucceeded {
		t.Fatalf("redelivered inbound = %+v, want succeeded", deliveries)
	}
	expectedTaskID := inboundDeliveryTaskID("ibd_retry")
	if deliveries[0].TaskID != expectedTaskID {
		t.Fatalf("redelivered task_id = %q, want deterministic %q", deliveries[0].TaskID, expectedTaskID)
	}
	if _, err := svc.repo.GetTaskByID(ctx, expectedTaskID); err != nil {
		t.Fatalf("redelivered task not found: %v", err)
	}

	var retriedAudits int
	if err := tdb.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE event_type = 'inbound_delivery_retried'").Scan(&retriedAudits); err != nil {
		t.Fatalf("count retry audits: %v", err)
	}
	if retriedAudits != 1 {
		t.Fatalf("inbound_delivery_retried audits = %d, want 1", retriedAudits)
	}
}

// TestRetryInboundDeliveryGuardsAndScope verifies the issue #421 guards for
// inbound: terminal-eligibility, live processing leases, and team scoping.
func TestRetryInboundDeliveryGuardsAndScope(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	seedTestInboundEndpoint(t, ctx, svc, "inb_scope", "pub_scope", "groups/platform/webhooks/inbound/x.yaml",
		`Do {{ .EventType }}`, "")
	seedTestInboundDelivery(t, ctx, svc, "ibd_scope", "inb_scope", "build.completed", []byte(`{"a":1}`))
	if _, err := svc.rawDB.ExecContext(ctx, testQuery(svc.dialect,
		`UPDATE inbound_deliveries SET team_id = 'team_a', status = 'failed_permanent', attempts = 2, error = 'bad' WHERE id = ?`,
		`UPDATE inbound_deliveries SET team_id = 'team_a', status = 'failed_permanent', attempts = 2, error = 'bad' WHERE id = $1`),
		"ibd_scope"); err != nil {
		t.Fatalf("force failed_permanent state: %v", err)
	}

	// A token scoped to another team must not see or mutate the delivery.
	if _, err := svc.RetryInboundDelivery(ctxWithTeam(ctx, "team_b"), "ibd_scope"); err == nil {
		t.Fatal("expected cross-team inbound retry to be rejected")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("cross-team retry error = %q, want not found", err)
	}

	// The owning team may retry it.
	if _, err := svc.RetryInboundDelivery(ctxWithTeam(ctx, "team_a"), "ibd_scope"); err != nil {
		t.Fatalf("owning-team retry: %v", err)
	}
	if _, err := svc.rawDB.ExecContext(ctx, testQuery(svc.dialect,
		`UPDATE inbound_deliveries SET status = 'succeeded', attempts = 2 WHERE id = ?`,
		`UPDATE inbound_deliveries SET status = 'succeeded', attempts = 2 WHERE id = $1`),
		"ibd_scope"); err != nil {
		t.Fatalf("force succeeded state: %v", err)
	}

	// A succeeded row is not retryable.
	if _, err := svc.RetryInboundDelivery(ctxWithAdmin(ctx), "ibd_scope"); err == nil {
		t.Fatal("expected retry of a succeeded inbound delivery to be rejected")
	} else if !strings.Contains(err.Error(), "cannot be retried from status") {
		t.Fatalf("succeeded retry error = %q, want a status explanation", err)
	}

	// A processing row with a live lease is not retryable.
	future := time.Now().UTC().Add(time.Hour)
	if _, err := svc.rawDB.ExecContext(ctx, testQuery(svc.dialect,
		`UPDATE inbound_deliveries SET status = 'processing', lease_expires_at = ?, updated_at = ? WHERE id = ?`,
		`UPDATE inbound_deliveries SET status = 'processing', lease_expires_at = $1, updated_at = $2 WHERE id = $3`),
		future, time.Now().UTC(), "ibd_scope"); err != nil {
		t.Fatalf("force processing state: %v", err)
	}
	if _, err := svc.RetryInboundDelivery(ctxWithAdmin(ctx), "ibd_scope"); err == nil {
		t.Fatal("expected retry of a live processing delivery to be rejected")
	} else if !strings.Contains(err.Error(), "cannot be retried from status") {
		t.Fatalf("processing retry error = %q, want a status explanation", err)
	}

	// Unknown ids report not found.
	if _, err := svc.RetryInboundDelivery(ctxWithAdmin(ctx), "ibd_missing"); err == nil {
		t.Fatal("expected unknown inbound delivery id to be rejected")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown id error = %q, want not found", err)
	}

	// Empty ids are rejected.
	if _, err := svc.RetryInboundDelivery(ctxWithAdmin(ctx), "  "); err == nil {
		t.Fatal("expected empty delivery id to be rejected")
	}
}
