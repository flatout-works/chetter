package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/flatout-works/chetter/internal/repository"
	"github.com/flatout-works/chetter/pkg/definitions"
)

// inboundWebhookTestYAML is a global inbound endpoint definition used by the
// integration tests. It references the CI secret by environment variable name
// only and creates a task whose prompt interpolates normalized metadata and
// the decoded JSON payload (issue #120 initial action model).
const inboundWebhookTestYAML = `name: ci-build-events
enabled: true
auth:
  type: hmac_sha256
  secret_env: CHETTER_WEBHOOK_TEST_SECRET
  signature_header: X-CI-Signature
  signature_prefix: sha256=
delivery_id_header: X-Delivery-ID
event_type_header: X-Event-Type
accepted_events:
  - build.completed
action:
  type: create_task
  prompt: Investigate CI event {{ .EventType }} for repo {{ .Payload.repository }} build {{ .Payload.build_number }}
  timeout_sec: 300
  team_name: platform
`

func createTestTeam(t *testing.T, ctx context.Context, svc *Service, id, name string) {
	t.Helper()
	now := time.Now().UTC()
	if err := svc.repo.CreateTeam(ctx, repository.CreateTeamParams{
		ID:        id,
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create team %s: %v", name, err)
	}
}

// seedTestInboundEndpoint inserts one materialized endpoint row directly.
func seedTestInboundEndpoint(t *testing.T, ctx context.Context, svc *Service, id, publicID, sourcePath, prompt string, acceptedEvents string) {
	t.Helper()
	now := time.Now().UTC()
	query := testQuery(svc.dialect,
		`INSERT INTO webhook_endpoints
		   (id, public_id, name, scope, team_id, source_path, enabled, auth_type, secret_env,
		    signature_header, signature_prefix, delivery_id_header, event_type_header,
		    accepted_events, action_type, action_prompt, action_agent, action_timeout_sec,
		    created_at, updated_at)
		 VALUES (?, ?, 'ci-build-events', 'global', '', ?, 1, 'hmac_sha256', 'CHETTER_WEBHOOK_TEST_SECRET',
		         'X-CI-Signature', 'sha256=', 'X-Delivery-ID', 'X-Event-Type', ?, 'create_task', ?, '', 300, ?, ?)`,
		`INSERT INTO webhook_endpoints
		   (id, public_id, name, scope, team_id, source_path, enabled, auth_type, secret_env,
		    signature_header, signature_prefix, delivery_id_header, event_type_header,
		    accepted_events, action_type, action_prompt, action_agent, action_timeout_sec,
		    created_at, updated_at)
		 VALUES ($1, $2, 'ci-build-events', 'global', '', $3, TRUE, 'hmac_sha256', 'CHETTER_WEBHOOK_TEST_SECRET',
		         'X-CI-Signature', 'sha256=', 'X-Delivery-ID', 'X-Event-Type', $4, 'create_task', $5, '', 300, $6, $7)`)
	var accepted any
	if acceptedEvents != "" {
		accepted = acceptedEvents
	}
	if _, err := svc.rawDB.ExecContext(ctx, query, id, publicID, sourcePath, accepted, prompt, now, now); err != nil {
		t.Fatalf("seed inbound endpoint: %v", err)
	}
}

// seedTestInboundDelivery inserts one pending delivery row directly.
func seedTestInboundDelivery(t *testing.T, ctx context.Context, svc *Service, id, endpointID, eventType string, payload []byte) {
	t.Helper()
	now := time.Now().UTC()
	query := testQuery(svc.dialect,
		`INSERT INTO inbound_deliveries
		   (id, endpoint_id, team_id, delivery_id, event_type, source_ip, payload, status, attempts,
		    max_attempts, created_at, updated_at, next_attempt_at)
		 VALUES (?, ?, '', 'dvr-1', ?, '127.0.0.1', ?, 'pending', 0, 3, ?, ?, ?)`,
		`INSERT INTO inbound_deliveries
		   (id, endpoint_id, team_id, delivery_id, event_type, source_ip, payload, status, attempts,
		    max_attempts, created_at, updated_at, next_attempt_at)
		 VALUES ($1, $2, '', 'dvr-1', $3, '127.0.0.1', $4, 'pending', 0, 3, $5, $6, $7)`)
	if _, err := svc.rawDB.ExecContext(ctx, query, id, endpointID, eventType, payload, now, now, now); err != nil {
		t.Fatalf("seed inbound delivery: %v", err)
	}
}

func inboundHMACSignature(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// TestInboundWebhooksEndToEnd covers issue #120 acceptance criteria end to
// end: Git-backed definition materialization with a stable public id,
// authenticated receipt returning 202 only after durable insertion, replay
// idempotency, worker processing into exactly one task, and removal on
// definition deletion. The same scenario runs against whichever dialect the
// integration harness provides (MySQL/TiDB or PostgreSQL).
func TestInboundWebhooksEndToEnd(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	t.Setenv("CHETTER_WEBHOOK_TEST_SECRET", "s3cr3t-value")
	ctx := context.Background()
	createTestTeam(t, ctx, svc, "team_platform", "platform")

	repoDir := createDefinitionsRepo(t)
	writeRepoFile(t, repoDir, "global/webhooks/inbound/ci-build-events.yaml", inboundWebhookTestYAML)
	runGit(t, repoDir, "add", ".")
	runGit(t, repoDir, "commit", "-m", "add inbound endpoint")
	svc.SetDefinitions(definitions.New(repoDir, "main", filepath.Join(t.TempDir(), "cache")))

	if _, err := svc.SyncDefinitions(ctx); err != nil {
		t.Fatalf("sync definitions: %v", err)
	}
	endpoints, err := svc.ListInboundEndpoints(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list endpoints: %v", err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("expected 1 materialized endpoint, got %d: %+v", len(endpoints), endpoints)
	}
	endpoint := endpoints[0]
	if endpoint.Name != "ci-build-events" || endpoint.Scope != "global" || endpoint.TeamID != "team_platform" {
		t.Fatalf("unexpected endpoint: %+v", endpoint)
	}
	if !strings.HasPrefix(endpoint.PublicURL, "/hooks/inbound/") || endpoint.PublicID == "" {
		t.Fatalf("endpoint missing public URL: %+v", endpoint)
	}
	if endpoint.SecretEnv != "CHETTER_WEBHOOK_TEST_SECRET" {
		t.Fatalf("secret env not exposed by name: %+v", endpoint)
	}
	if !endpoint.SecretConfigured {
		t.Error("secret_configured should be true when the environment variable is set")
	}
	if len(endpoint.AcceptedEvents) != 1 || endpoint.AcceptedEvents[0] != "build.completed" {
		t.Errorf("accepted events = %v", endpoint.AcceptedEvents)
	}

	// A second sync keeps the runtime identity (public_id) stable.
	firstPublicID := endpoint.PublicID
	if _, err := svc.SyncDefinitions(ctx); err != nil {
		t.Fatalf("resync definitions: %v", err)
	}
	endpoints, err = svc.ListInboundEndpoints(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list endpoints after resync: %v", err)
	}
	if len(endpoints) != 1 || endpoints[0].PublicID != firstPublicID {
		t.Fatalf("public id changed across sync: %+v", endpoints)
	}

	// Send an authenticated request through the real receiver.
	handler := NewInboundWebhookReceiver(svc.rawDB, svc.dialect, svc)
	if handler == nil {
		t.Fatal("inbound receiver not built")
	}
	body := `{"repository":"acme/web","build_number":42}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/inbound/"+endpoint.PublicID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CI-Signature", inboundHMACSignature("s3cr3t-value", body))
	req.Header.Set("X-Delivery-ID", "dvr-e2e-1")
	req.Header.Set("X-Event-Type", "build.completed")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("receiver status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}

	deliveries, err := svc.ListInboundDeliveries(ctx, "", "", 10, 0)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != inboundDeliveryStatusPending {
		t.Fatalf("expected one pending delivery, got %+v", deliveries)
	}
	deliveryID := deliveries[0].ID
	expectedTaskID := inboundDeliveryTaskID(deliveryID)

	// Process the inbox: one task must be created for the delivery.
	if err := svc.processDueInboundDeliveries(ctx); err != nil {
		t.Fatalf("process inbound deliveries: %v", err)
	}
	deliveries, err = svc.ListInboundDeliveries(ctx, "", "", 10, 0)
	if err != nil {
		t.Fatalf("list deliveries after processing: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != inboundDeliveryStatusSucceeded {
		t.Fatalf("delivery did not succeed: %+v", deliveries)
	}
	if deliveries[0].TaskID != expectedTaskID {
		t.Fatalf("delivery task_id = %q, want %q", deliveries[0].TaskID, expectedTaskID)
	}
	task, err := svc.repo.GetTaskByID(ctx, expectedTaskID)
	if err != nil {
		t.Fatalf("created task not found: %v", err)
	}
	if task.TeamID.String != "team_platform" {
		t.Errorf("task team = %q, want team_platform", task.TeamID.String)
	}
	if !strings.Contains(task.Prompt, "for repo acme/web build 42") {
		t.Errorf("task prompt = %q, want rendered payload fields", task.Prompt)
	}
	if !strings.Contains(task.Prompt, "build.completed") {
		t.Errorf("task prompt = %q, want event type interpolated", task.Prompt)
	}

	// Replaying the same delivery_id is idempotent: 202 and no new task.
	req = httptest.NewRequest(http.MethodPost, "/hooks/inbound/"+endpoint.PublicID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CI-Signature", inboundHMACSignature("s3cr3t-value", body))
	req.Header.Set("X-Delivery-ID", "dvr-e2e-1")
	req.Header.Set("X-Event-Type", "build.completed")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("replay status = %d, want 202", rec.Code)
	}
	deliveries, err = svc.ListInboundDeliveries(ctx, "", "", 10, 0)
	if err != nil {
		t.Fatalf("list deliveries after replay: %v", err)
	}
	if len(deliveries) != 1 {
		t.Fatalf("replay created a second delivery row: %+v", deliveries)
	}
	if _, err := svc.repo.GetTaskByID(ctx, expectedTaskID); err != nil {
		t.Fatalf("task disappeared: %v", err)
	}

	// Removing the definition removes the endpoint and stops new receipts.
	if err := os.Remove(filepath.Join(repoDir, "global/webhooks/inbound/ci-build-events.yaml")); err != nil {
		t.Fatalf("remove definition: %v", err)
	}
	runGit(t, repoDir, "add", "-A")
	runGit(t, repoDir, "commit", "-m", "remove inbound endpoint")
	if _, err := svc.SyncDefinitions(ctx); err != nil {
		t.Fatalf("sync after removal: %v", err)
	}
	endpoints, err = svc.ListInboundEndpoints(ctx, 10, 0)
	if err != nil {
		t.Fatalf("list endpoints after removal: %v", err)
	}
	if len(endpoints) != 0 {
		t.Fatalf("endpoint not removed: %+v", endpoints)
	}
	req = httptest.NewRequest(http.MethodPost, "/hooks/inbound/"+firstPublicID, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CI-Signature", inboundHMACSignature("s3cr3t-value", body))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("removed endpoint status = %d, want 404", rec.Code)
	}
}

// TestInboundDeliveryRetryCannotDuplicateTask verifies issue #120's core
// guarantee: when a worker crashes after creating the task but before marking
// the delivery succeeded, the reclaimed delivery resolves the existing task
// instead of creating a second one.
func TestInboundDeliveryRetryCannotDuplicateTask(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	seedTestInboundEndpoint(t, ctx, svc, "inb_e2e", "pub_e2e", "global/webhooks/inbound/ci.yaml",
		`Investigate {{ .EventType }} {{ .Payload.repository }}`, `["build.completed"]`)
	seedTestInboundDelivery(t, ctx, svc, "ibd_e2e", "inb_e2e", "build.completed", []byte(`{"repository":"acme/web"}`))

	if err := svc.processDueInboundDeliveries(ctx); err != nil {
		t.Fatalf("first processing: %v", err)
	}
	expectedTaskID := inboundDeliveryTaskID("ibd_e2e")
	if _, err := svc.repo.GetTaskByID(ctx, expectedTaskID); err != nil {
		t.Fatalf("task after first processing: %v", err)
	}

	// Simulate a crash between task creation and the delivery marking: the
	// delivery is still processing with an expired lease and no task link.
	now := time.Now().UTC()
	past := now.Add(-2 * time.Hour)
	query := testQuery(svc.dialect,
		`UPDATE inbound_deliveries SET status = 'processing', task_id = NULL, lease_expires_at = ?, updated_at = ? WHERE id = 'ibd_e2e'`,
		`UPDATE inbound_deliveries SET status = 'processing', task_id = NULL, lease_expires_at = $1, updated_at = $2 WHERE id = 'ibd_e2e'`)
	if _, err := svc.rawDB.ExecContext(ctx, query, past, now); err != nil {
		t.Fatalf("force crash state: %v", err)
	}

	if err := svc.processDueInboundDeliveries(ctx); err != nil {
		t.Fatalf("reprocessing after crash: %v", err)
	}
	deliveries, err := svc.ListInboundDeliveries(ctx, "", "", 10, 0)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != inboundDeliveryStatusSucceeded {
		t.Fatalf("delivery not succeeded after reclaim: %+v", deliveries)
	}
	if deliveries[0].TaskID != expectedTaskID {
		t.Fatalf("reclaimed delivery task_id = %q, want %q", deliveries[0].TaskID, expectedTaskID)
	}
	// Exactly one task exists with the deterministic id.
	count := 0
	countQuery := testQuery(svc.dialect,
		`SELECT COUNT(*) FROM tasks WHERE id = ?`,
		`SELECT COUNT(*) FROM tasks WHERE id = $1`)
	if err := svc.rawDB.QueryRowContext(ctx, countQuery, expectedTaskID).Scan(&count); err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if count != 1 {
		t.Fatalf("task count = %d, want exactly 1", count)
	}
}

// TestInboundDeliveryPermanentFailures verifies the non-retryable outcomes:
// an unrenderable action prompt and a disabled endpoint both land in
// failed_permanent, and delivery/task correlation stays clean.
func TestInboundDeliveryPermanentFailures(t *testing.T) {
	svc, _, cleanup := newServiceForTest(t)
	defer cleanup()
	ctx := context.Background()

	// Broken static template: can never render -> failed_permanent.
	seedTestInboundEndpoint(t, ctx, svc, "inb_bad", "pub_bad", "global/webhooks/inbound/bad.yaml", "{{", "")
	seedTestInboundDelivery(t, ctx, svc, "ibd_bad", "inb_bad", "build.completed", []byte(`{"a":1}`))
	if err := svc.processDueInboundDeliveries(ctx); err != nil {
		t.Fatalf("process broken template: %v", err)
	}
	deliveries, err := svc.ListInboundDeliveries(ctx, "", "", 10, 0)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	if len(deliveries) != 1 || deliveries[0].Status != inboundDeliveryStatusFailedPermanent {
		t.Fatalf("broken template delivery = %+v, want failed_permanent", deliveries)
	}
	if !strings.Contains(deliveries[0].Error, "render action prompt") {
		t.Errorf("error = %q", deliveries[0].Error)
	}

	// Endpoint removed while the delivery was queued -> failed_permanent.
	seedTestInboundEndpoint(t, ctx, svc, "inb_gone", "pub_gone", "global/webhooks/inbound/gone.yaml", "Do stuff", "")
	seedTestInboundDelivery(t, ctx, svc, "ibd_gone", "inb_gone", "build.completed", []byte(`{"a":1}`))
	if _, err := svc.rawDB.ExecContext(ctx, testQuery(svc.dialect,
		`DELETE FROM webhook_endpoints WHERE id = ?`,
		`DELETE FROM webhook_endpoints WHERE id = $1`), "inb_gone"); err != nil {
		t.Fatalf("delete endpoint: %v", err)
	}
	if err := svc.processDueInboundDeliveries(ctx); err != nil {
		t.Fatalf("process orphaned delivery: %v", err)
	}
	deliveries, err = svc.ListInboundDeliveries(ctx, "", "", 10, 0)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	byID := map[string]inboundDeliveryRecord{}
	for _, d := range deliveries {
		byID[d.ID] = d
	}
	if d, ok := byID["ibd_gone"]; !ok || d.Status != inboundDeliveryStatusFailedPermanent {
		t.Fatalf("orphaned delivery = %+v, want failed_permanent", d)
	}
	if d, ok := byID["ibd_gone"]; ok && !strings.Contains(d.Error, "endpoint no longer exists") {
		t.Errorf("orphaned delivery error = %q", d.Error)
	}
}
