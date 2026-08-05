package webapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/flatout-works/chetter/gen/proto/api/v1"
	apiv1connect "github.com/flatout-works/chetter/gen/proto/api/v1/apiv1connect"
	"github.com/flatout-works/chetter/internal/config"
	"github.com/flatout-works/chetter/internal/service"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/internal/webhook"
)

// TestWebAPITestTriggerValidation covers the manual test-run endpoint for
// external-event triggers (issue #271) on a server without a GitHub App:
// cron triggers are rejected and GitHub-dependent triggers fail with a clear
// configuration error before any GitHub API call is attempted.
func TestWebAPITestTriggerValidation(t *testing.T) {
	server, cleanup := newWebAPITestServer(t)
	defer cleanup()
	ctx := context.Background()
	triggers := apiv1connect.NewTriggerServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)

	if _, err := triggers.CreateTrigger(ctx, connect.NewRequest(&apiv1.CreateTriggerRequest{
		Name:        "cron-only",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "cron task",
	})); err != nil {
		t.Fatalf("CreateTrigger cron: %v", err)
	}
	if _, err := triggers.CreateTrigger(ctx, connect.NewRequest(&apiv1.CreateTriggerRequest{
		Name:        "review-test",
		TriggerType: store.TriggerTypePRReview,
		Repo:        "acme/one",
		Event:       "opened",
		Prompt:      "review it",
		Agent:       "pr-reviewer",
	})); err != nil {
		t.Fatalf("CreateTrigger pr_review: %v", err)
	}

	// Cron triggers use Run Now, not the test-run flow.
	_, err := triggers.TestTrigger(ctx, connect.NewRequest(&apiv1.TestTriggerRequest{
		Name:  "cron-only",
		Repo:  "acme/one",
		Event: "opened",
	}))
	if err == nil || !strings.Contains(err.Error(), "test runs are only supported for pr_review and issue triggers") {
		t.Fatalf("cron TestTrigger error = %v, want unsupported-type message", err)
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("cron TestTrigger code = %s, want failed_precondition", connect.CodeOf(err))
	}

	// A missing GitHub App must produce a clear error instead of a panic or a
	// confusing GitHub API failure.
	_, err = triggers.TestTrigger(ctx, connect.NewRequest(&apiv1.TestTriggerRequest{
		Name:     "review-test",
		Repo:     "acme/one",
		Event:    "opened",
		PrNumber: 42,
	}))
	if err == nil || !strings.Contains(err.Error(), "github app is not configured") {
		t.Fatalf("no-github TestTrigger error = %v, want configuration error", err)
	}
}

// newGitHubTestManager builds a webhook.Manager pointed at a fake GitHub API
// server, mirroring the webhook package's own test helper.
func newGitHubTestManager(t *testing.T, apiBase string) *webhook.Manager {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
	manager, err := webhook.NewManager(123, encoded, webhook.WithAPIBaseURL(apiBase))
	if err != nil {
		t.Fatalf("create GitHub manager: %v", err)
	}
	return manager
}

// fakeGitHubServer answers the GitHub API calls the manual test-run flow
// makes: repository installation resolution, installation access tokens, and
// authoritative PR/issue metadata.
func fakeGitHubServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/app/installations/"):
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"token":"fake-token","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/installation"):
			_, _ = w.Write([]byte(`{"id":111}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/pulls/"):
			_, _ = w.Write([]byte(`{"number":42,"state":"open","head":{"ref":"feature/x","repo":{"clone_url":"https://github.com/acme/one.git"}},"base":{"ref":"main"}}`))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/issues/"):
			_, _ = w.Write([]byte(`{"number":7,"state":"open","title":"Broken login","body":"Cannot log in","html_url":"https://github.com/acme/one/issues/7","labels":[{"name":"bug"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
}

// newWebAPITestServerWithGitHub is newWebAPITestServer with a GitHub App
// manager wired into the service so the manual test-run flow can resolve
// repository installations and fetch authoritative metadata.
func newWebAPITestServerWithGitHub(t *testing.T, gh *httptest.Server) (*httptest.Server, func()) {
	t.Helper()
	manager := newGitHubTestManager(t, gh.URL)
	tdb, cleanupDB := webAPITestDB.NewTestDB(t)
	cfg := config.Config{DefaultAgentImage: "runner:latest", DefaultTaskTimeoutSec: 600}
	st, err := store.Open(tdb.DSN, tdb.Dialect())
	if err != nil {
		cleanupDB()
		t.Fatalf("store.Open: %v", err)
	}
	now := time.Now().UTC()
	if _, err := tdb.DB.Exec(testQuery(tdb.Dialect(),
		`INSERT INTO git_identities (id, team_id, name, git_author_name, git_author_email, credential_type, is_default, created_at, updated_at) VALUES (?, '', 'primary-bot', 'Primary Bot', 'primary-bot@example.com', 'github_app', true, ?, ?)`,
		`INSERT INTO git_identities (id, team_id, name, git_author_name, git_author_email, credential_type, is_default, created_at, updated_at) VALUES ($1, '', 'primary-bot', 'Primary Bot', 'primary-bot@example.com', 'github_app', true, $2, $3)`,
	), "gid_primary", now, now); err != nil {
		_ = st.Close()
		cleanupDB()
		t.Fatalf("seed default Git identity: %v", err)
	}
	svc := service.New(cfg, st)
	svc.SetGitHubManager(manager)
	// Seed active agent definitions so triggers with an agent can submit tasks.
	// Each definition resolves to the default "primary-bot" Git identity.
	now2 := time.Now().UTC()
	for _, agent := range []string{"pr-reviewer", "issue-triage"} {
		if _, err := tdb.DB.Exec(testQuery(tdb.Dialect(),
			`INSERT INTO definitions (id, source_id, definition_type, name, scope, path, source_commit, content_hash, content, active, created_at, updated_at) VALUES (?, ?, 'agent', ?, 'global', ?, ?, ?, ?, true, ?, ?)`,
			`INSERT INTO definitions (id, source_id, definition_type, name, scope, path, source_commit, content_hash, content, active, created_at, updated_at) VALUES ($1, $2, 'agent', $3, 'global', $4, $5, $6, $7, true, $8, $9)`,
		), "def_"+agent, "src_test", agent, "agents/"+agent+".md", "test", strings.Repeat("1", 64), "---\nidentity: primary-bot\n---\n# "+agent+"\n", now2, now2); err != nil {
			_ = st.Close()
			cleanupDB()
			t.Fatalf("seed agent definition %s: %v", agent, err)
		}
	}
	bus := NewEventBus()
	mux := http.NewServeMux()
	RegisterHandlers(mux, NewHandlers(svc, bus), webAPITestAdminToken, st.DB(), nil, nil)
	server := httptest.NewServer(mux)
	return server, func() {
		server.Close()
		bus.CloseAll()
		_ = st.Close()
		cleanupDB()
	}
}

// TestWebAPITestTriggerPRReview verifies the full pr_review manual test-run
// path: the server fetches authoritative PR metadata from GitHub, dispatches
// the same review task configuration as the real webhook, and stamps the task
// as a manual test run.
func TestWebAPITestTriggerPRReview(t *testing.T) {
	gh := fakeGitHubServer(t)
	defer gh.Close()
	server, cleanup := newWebAPITestServerWithGitHub(t, gh)
	defer cleanup()
	ctx := context.Background()
	triggers := apiv1connect.NewTriggerServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)
	tasks := apiv1connect.NewTaskServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)

	if _, err := triggers.CreateTrigger(ctx, connect.NewRequest(&apiv1.CreateTriggerRequest{
		Name:        "deep-review",
		TriggerType: store.TriggerTypePRReview,
		Repo:        "acme/one",
		Event:       "opened",
		Prompt:      "Deep review of PR {{PR_NUMBER}} on {{BASE_REF}}..{{HEAD_REF}}",
		Agent:       "pr-reviewer",
	})); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	// The web API does not persist the event filter for pr_review triggers at
	// creation, so set it explicitly via UpdateTrigger to exercise the
	// event-mismatch rejection below.
	if _, err := triggers.UpdateTrigger(ctx, connect.NewRequest(&apiv1.UpdateTriggerRequest{
		Name:  "deep-review",
		Event: "opened",
	})); err != nil {
		t.Fatalf("UpdateTrigger event: %v", err)
	}

	// Unsupported event is rejected before any GitHub API call.
	_, err := triggers.TestTrigger(ctx, connect.NewRequest(&apiv1.TestTriggerRequest{
		Name:     "deep-review",
		Repo:     "acme/one",
		Event:    "merged",
		PrNumber: 42,
	}))
	if err == nil || !strings.Contains(err.Error(), "not supported for pr_review test runs") {
		t.Fatalf("bad event error = %v, want unsupported-event message", err)
	}

	// Event mismatch with the trigger's configured event is rejected.
	_, err = triggers.TestTrigger(ctx, connect.NewRequest(&apiv1.TestTriggerRequest{
		Name:     "deep-review",
		Repo:     "acme/one",
		Event:    "labeled",
		PrNumber: 42,
	}))
	if err == nil || !strings.Contains(err.Error(), "does not respond to") {
		t.Fatalf("event mismatch error = %v, want does-not-respond message", err)
	}

	resp, err := triggers.TestTrigger(ctx, connect.NewRequest(&apiv1.TestTriggerRequest{
		Name:     "deep-review",
		Repo:     "acme/one",
		Event:    "opened",
		PrNumber: 42,
	}))
	if err != nil {
		t.Fatalf("TestTrigger pr_review: %v", err)
	}
	if len(resp.Msg.TaskIds) != 1 {
		t.Fatalf("task ids = %v, want exactly one", resp.Msg.TaskIds)
	}
	if resp.Msg.Trigger.GetName() != "deep-review" {
		t.Fatalf("trigger name = %q, want deep-review", resp.Msg.Trigger.GetName())
	}

	got, err := tasks.GetTask(ctx, connect.NewRequest(&apiv1.GetTaskRequest{TaskId: resp.Msg.TaskIds[0]}))
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task := got.Msg.Task
	if task.GetSubmissionSource() != "trigger_test" {
		t.Fatalf("submission_source = %q, want trigger_test", task.GetSubmissionSource())
	}
	if task.GetTriggerName() != "deep-review" || task.GetTriggerType() != "pr_review" {
		t.Fatalf("trigger attribution = %q/%q, want deep-review/pr_review", task.GetTriggerName(), task.GetTriggerType())
	}
	// Authoritative PR metadata fetched from GitHub, not client-supplied.
	if !strings.Contains(task.GetPrompt(), "Deep review of PR 42 on main..feature/x") {
		t.Fatalf("prompt = %q, want authoritative PR refs", task.GetPrompt())
	}
	if task.GetEnv()["PR_NUMBER"] != "42" || task.GetEnv()["GITHUB_REPO"] != "acme/one" {
		t.Fatalf("review env = %v", task.GetEnv())
	}
	if task.GetGitUrl() != "https://github.com/acme/one.git" || task.GetGitRef() != "feature/x" {
		t.Fatalf("git url/ref = %q/%q, want PR head clone/ref", task.GetGitUrl(), task.GetGitRef())
	}
}

// TestWebAPITestTriggerIssue verifies the issue manual test-run path: issue
// metadata is fetched from GitHub, match_labels are evaluated (against
// simulated labels when provided, the issue's real labels otherwise), and the
// task carries the same trigger configuration as a real issues webhook.
func TestWebAPITestTriggerIssue(t *testing.T) {
	gh := fakeGitHubServer(t)
	defer gh.Close()
	server, cleanup := newWebAPITestServerWithGitHub(t, gh)
	defer cleanup()
	ctx := context.Background()
	triggers := apiv1connect.NewTriggerServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)
	tasks := apiv1connect.NewTaskServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)

	if _, err := triggers.CreateTrigger(ctx, connect.NewRequest(&apiv1.CreateTriggerRequest{
		Name:        "bug-triage",
		TriggerType: store.TriggerTypeIssue,
		Repo:        "acme/one",
		Event:       "opened",
		MatchLabels: []string{"bug"},
		Prompt:      "Triage issue {{ISSUE_NUMBER}}",
		Agent:       "issue-triage",
	})); err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}

	// Simulated labels that do not match the trigger's match_labels are rejected.
	_, err := triggers.TestTrigger(ctx, connect.NewRequest(&apiv1.TestTriggerRequest{
		Name:        "bug-triage",
		Repo:        "acme/one",
		Event:       "opened",
		IssueNumber: 7,
		Labels:      []string{"feature"},
	}))
	if err == nil || !strings.Contains(err.Error(), "requires one of labels") {
		t.Fatalf("label mismatch error = %v, want requires-one-of-labels message", err)
	}

	// Simulated labels that match dispatch a task.
	resp, err := triggers.TestTrigger(ctx, connect.NewRequest(&apiv1.TestTriggerRequest{
		Name:        "bug-triage",
		Repo:        "acme/one",
		Event:       "opened",
		IssueNumber: 7,
		Labels:      []string{"bug"},
	}))
	if err != nil {
		t.Fatalf("TestTrigger issue with labels: %v", err)
	}
	if len(resp.Msg.TaskIds) != 1 {
		t.Fatalf("task ids = %v, want exactly one", resp.Msg.TaskIds)
	}
	got, err := tasks.GetTask(ctx, connect.NewRequest(&apiv1.GetTaskRequest{TaskId: resp.Msg.TaskIds[0]}))
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	task := got.Msg.Task
	if task.GetSubmissionSource() != "trigger_test" {
		t.Fatalf("submission_source = %q, want trigger_test", task.GetSubmissionSource())
	}
	if !strings.Contains(task.GetPrompt(), "Triage issue 7") {
		t.Fatalf("prompt = %q, want trigger prompt", task.GetPrompt())
	}
	if task.GetEnv()["ISSUE_NUMBER"] != "7" || task.GetEnv()["ISSUE_ACTION"] != "opened" {
		t.Fatalf("issue env = %v", task.GetEnv())
	}

	// Without simulated labels, the issue's real GitHub labels are used for
	// match_labels evaluation. The fake issue carries the "bug" label.
	resp2, err := triggers.TestTrigger(ctx, connect.NewRequest(&apiv1.TestTriggerRequest{
		Name:        "bug-triage",
		Repo:        "acme/one",
		Event:       "opened",
		IssueNumber: 7,
	}))
	if err != nil {
		t.Fatalf("TestTrigger issue with real labels: %v", err)
	}
	if len(resp2.Msg.TaskIds) != 1 {
		t.Fatalf("real-label task ids = %v, want exactly one", resp2.Msg.TaskIds)
	}
}
