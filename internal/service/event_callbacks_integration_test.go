package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/internal/ssrf"
	"github.com/flatout-works/chetter/internal/testdb"
)

// TestCreateTaskCallbackRecordsProvenance verifies that a create_task callback
// stamps the spawned task with its parent task and chain depth (issue #312).
func TestCreateTaskCallbackRecordsProvenance(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	// Callback context variables are server-owned rather than user input.
	svc.cfg.EnvValidation.BlockedPrefixes = nil

	source, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "source task", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit source task: %v", err)
	}
	callback := repository.EventCallback{
		Name:         "follow-up",
		ActionType:   EventCallbackActionCreateTask,
		ActionConfig: json.RawMessage(`{"prompt":"follow up"}`),
	}
	if err := svc.runCreateTaskCallback(ctx, TaskEventCallbackContext{ID: "evt_1", TaskID: source.ID, EventType: "task.completed"}, callback); err != nil {
		t.Fatalf("run create-task callback: %v", err)
	}

	var child repository.Task
	child, err = taskByTriggerName(ctx, tdb, svc, callback.Name)
	if err != nil {
		t.Fatalf("load callback task: %v", err)
	}
	if !child.CallbackParentTaskID.Valid || child.CallbackParentTaskID.String != source.ID {
		t.Fatalf("callback_parent_task_id = %+v, want %q", child.CallbackParentTaskID, source.ID)
	}
	if child.CallbackDepth != 1 {
		t.Fatalf("callback_depth = %d, want 1", child.CallbackDepth)
	}
	if child.TriggerName.String != callback.Name || child.SubmissionSource != "event_callback" {
		t.Fatalf("callback provenance = (%q, %q)", child.TriggerName.String, child.SubmissionSource)
	}
}

// TestCreateTaskCallbackRecursionLimit is the regression test for issue #312:
// a task.completed -> create_task callback with a self-completing prompt must
// terminate after the depth limit instead of growing the queue unboundedly.
// The chain is driven to completion by re-dispatching the callback on each
// spawned task; the spawn that would exceed the limit is rejected, a
// task.callback_rejected event is recorded on the parent task, and an audit
// event is emitted. The callback itself stays enabled.
func TestCreateTaskCallbackRecursionLimit(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()
	// Callback context variables are server-owned rather than user input.
	svc.cfg.EnvValidation.BlockedPrefixes = nil
	svc.cfg.CallbackMaxDepth = 2

	source, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "source task", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit source task: %v", err)
	}
	callback := repository.EventCallback{
		Name:         "self-loop",
		ActionType:   EventCallbackActionCreateTask,
		ActionConfig: json.RawMessage(`{"prompt":"complete quickly"}`),
	}

	// Drive the recursion loop exactly as the dispatch path would: each
	// spawned task completes and fires task.completed, which re-runs the same
	// callback. The guard must stop the chain at depth 2 instead of spawning
	// tasks forever.
	current := source.ID
	var rejectedTaskID string
	for range 10 {
		event := TaskEventCallbackContext{ID: "evt_loop", TaskID: current, EventType: "task.completed"}
		if err := svc.runCreateTaskCallback(ctx, event, callback); err != nil {
			if !strings.Contains(err.Error(), eventCallbackRecursionError) {
				t.Fatalf("callback error = %v, want %q", err, eventCallbackRecursionError)
			}
			rejectedTaskID = current
			break
		}
		child, err := taskByTriggerName(ctx, tdb, svc, callback.Name)
		if err != nil {
			t.Fatalf("load spawned task: %v", err)
		}
		if child.ID == current {
			t.Fatalf("callback spawned a duplicate task %q", child.ID)
		}
		current = child.ID
	}
	if rejectedTaskID == "" {
		t.Fatal("callback chain did not terminate at the depth limit")
	}

	// The queue must not have grown unboundedly: source + 2 allowed spawns.
	var total int
	if err := tdb.DB.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&total); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if total != 3 {
		t.Fatalf("task count = %d, want 3 (source + 2 depth-limited spawns)", total)
	}

	// The rejected spawn left a task.callback_rejected event on the parent
	// task with the recursion-limit error in its payload.
	var eventType, status string
	var payload []byte
	err = tdb.DB.QueryRow(testQuery(tdb.Dialect(),
		"SELECT event_type, status, payload FROM task_events WHERE task_id = ? AND event_type = ? ORDER BY created_at DESC LIMIT 1",
		"SELECT event_type, status, payload FROM task_events WHERE task_id = $1 AND event_type = $2 ORDER BY created_at DESC LIMIT 1"),
		rejectedTaskID, eventCallbackRejectedEvent).Scan(&eventType, &status, &payload)
	if err != nil {
		t.Fatalf("load rejection event: %v", err)
	}
	if eventType != eventCallbackRejectedEvent || status != "error" {
		t.Fatalf("rejection event = (%q, %q), want (%q, error)", eventType, status, eventCallbackRejectedEvent)
	}
	var eventPayload map[string]any
	if err := json.Unmarshal(payload, &eventPayload); err != nil {
		t.Fatalf("parse rejection payload: %v", err)
	}
	if eventPayload["error"] != eventCallbackRecursionError {
		t.Fatalf("rejection payload error = %v, want %q", eventPayload["error"], eventCallbackRecursionError)
	}

	// An audit event was emitted for the rejected spawn.
	var auditCount int
	if err := tdb.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE event_type = 'event_callback_recursion_limit'").Scan(&auditCount); err != nil {
		t.Fatalf("count audit events: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("audit events = %d, want 1", auditCount)
	}

	// The callback itself is not disabled: spawning from a fresh depth-0 task
	// still works after the loop was cut.
	fresh, err := svc.SubmitTask(ctx, SubmitTaskRequest{Prompt: "fresh source", AgentImage: "runner:latest"})
	if err != nil {
		t.Fatalf("submit fresh source task: %v", err)
	}
	if err := svc.runCreateTaskCallback(ctx, TaskEventCallbackContext{ID: "evt_fresh", TaskID: fresh.ID, EventType: "task.completed"}, callback); err != nil {
		t.Fatalf("callback after cut loop should still be enabled: %v", err)
	}
}

// taskByTriggerName returns the most recently created task recorded with the
// given trigger (callback) name.
func taskByTriggerName(ctx context.Context, tdb *testdb.TestDB, svc *Service, triggerName string) (repository.Task, error) {
	rows, err := svc.repo.ListTasksByStatus(ctx, repository.ListTasksByStatusParams{
		StatusFilter:      "",
		TriggerNameFilter: sql.NullString{String: triggerName, Valid: true},
		Limit:             1,
		Offset:            0,
	})
	if err != nil {
		return repository.Task{}, err
	}
	if len(rows) == 0 {
		return repository.Task{}, sql.ErrNoRows
	}
	return rows[0], nil
}

// TestCreateWebhookCallbackRejectsUnsafeDestination is the create/update-time
// half of the SSRF-safe destination policy (issue #337): a webhook/slack
// callback whose destination violates the policy is rejected with a clear
// error at create time, public destinations keep working, and rejections are
// recorded in the audit log.
func TestCreateWebhookCallbackRejectsUnsafeDestination(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	unsafe := []struct {
		name string
		url  string
	}{
		{"metadata-http", "http://169.254.169.254/latest/meta-data/"},
		{"metadata-https", "https://metadata.google.internal/computeMetadata/v1/"},
		{"private-ip", "https://10.0.0.5/internal"},
		{"loopback-http", "http://127.0.0.1:8080/hook"},
	}
	for _, tc := range unsafe {
		if _, err := svc.CreateEventCallback(ctx, EventCallbackInput{
			Name:         tc.name,
			EventType:    "task.completed",
			ActionType:   EventCallbackActionWebhook,
			ActionConfig: json.RawMessage(`{"url":"` + tc.url + `"}`),
			Enabled:      true,
		}); err == nil {
			t.Errorf("callback %q with url %q should be rejected by the destination policy", tc.name, tc.url)
		}
	}

	// Public destinations are unaffected (validation does no DNS lookup).
	if _, err := svc.CreateEventCallback(ctx, EventCallbackInput{
		Name:         "public-hook",
		EventType:    "task.completed",
		ActionType:   EventCallbackActionWebhook,
		ActionConfig: json.RawMessage(`{"url":"https://hooks.example.com/cb"}`),
		Enabled:      true,
	}); err != nil {
		t.Fatalf("public destination rejected: %v", err)
	}

	var auditCount int
	if err := tdb.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE event_type = 'event_callback_destination_rejected'").Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount < len(unsafe) {
		t.Errorf("expected at least %d destination-rejection audit events, got %d", len(unsafe), auditCount)
	}
}

// TestWebhookCallbackDeliveryUsesSafeClient exercises the delivery path of the
// SSRF-safe destination policy (issue #337): hardened defaults refuse a
// callback pointed at a local/loopback destination (and audit the rejection),
// while the explicit operator overrides (loopback development mode) let the
// same callback deliver to the local httptest receiver.
func TestWebhookCallbackDeliveryUsesSafeClient(t *testing.T) {
	svc, tdb, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	callback := repository.EventCallback{
		ID:           "ecb_delivery",
		Name:         "delivery-hook",
		ActionType:   EventCallbackActionWebhook,
		ActionConfig: json.RawMessage(`{"url":"` + server.URL + `/cb","method":"POST"}`),
		Enabled:      true,
	}
	event := TaskEventCallbackContext{ID: "evt_1", TaskID: "task_1", EventType: "task.completed"}

	t.Run("hardened default rejects loopback destination", func(t *testing.T) {
		err := svc.runWebhookCallback(ctx, event, callback)
		if err == nil {
			t.Fatal("expected destination-policy error for hardened default")
		}
		var polErr *ssrf.Error
		if !errors.As(err, &polErr) {
			t.Fatalf("error %v is not an ssrf.Error", err)
		}
		var auditCount int
		if err := tdb.DB.QueryRow("SELECT COUNT(*) FROM audit_log WHERE event_type = 'event_callback_destination_rejected'").Scan(&auditCount); err != nil {
			t.Fatalf("count audit rows: %v", err)
		}
		if auditCount == 0 {
			t.Error("expected a destination-rejection audit event at delivery")
		}
	})

	t.Run("explicit operator override delivers to local receiver", func(t *testing.T) {
		svc.cfg.WebhookAllowHTTP = true
		svc.cfg.WebhookAllowPrivate = true
		if err := svc.runWebhookCallback(ctx, event, callback); err != nil {
			t.Fatalf("delivery with explicit overrides failed: %v", err)
		}
		select {
		case path := <-received:
			if path != "/cb" {
				t.Errorf("received path %q, want /cb", path)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("receiver never got the callback delivery")
		}
	})
}
