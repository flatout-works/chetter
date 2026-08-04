package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	runnerv1 "github.com/flatout-works/chetter/gen/proto/runner/v1"
	"github.com/flatout-works/chetter/internal/data"
	"github.com/flatout-works/chetter/internal/testdb"
	"github.com/flatout-works/chetter/internal/webhook"
)

func newRunnerGitHubTestManager(t *testing.T, apiBase string) *webhook.Manager {
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

func insertGitHubRPCTask(t *testing.T, q data.Repository, tdb *testdb.TestDB, taskID, repo string, installationID int64) {
	t.Helper()
	insertPendingTask(t, q, taskID, "create an issue", "runner:latest")
	var installation any
	if installationID > 0 {
		installation = installationID
	}
	if _, err := tdb.DB.Exec(testQuery(tdb.Dialect(),
		"UPDATE tasks SET github_repo = ?, github_installation_id = ? WHERE id = ?",
		"UPDATE tasks SET github_repo = $1, github_installation_id = $2 WHERE id = $3"), repo, installation, taskID); err != nil {
		t.Fatalf("set task GitHub metadata: %v", err)
	}
}

func activateGitHubRPCTask(t *testing.T, q data.Repository, taskID, runnerID string, leaseExpiresAt time.Time) {
	t.Helper()
	now := time.Now().UTC()
	markTaskRunning(t, q, taskID, now)
	markPendingExecutionAttemptClaimed(t, q, taskID, runnerID, now, leaseExpiresAt)
}

func TestGitHubCreateIssueAuthorizesAndPinsInstallation(t *testing.T) {
	rpc, q, tdb, cleanup := newRPCTestService(t)
	defer cleanup()

	var calls atomic.Int64
	var issueBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/Acme/Repo/installation":
			_, _ = w.Write([]byte(`{"id":111}`))
		case r.Method == http.MethodPost && r.URL.Path == "/app/installations/111/access_tokens":
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"token":"installation-111","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
		case r.Method == http.MethodPost && r.URL.Path == "/repos/Acme/Repo/issues":
			if got := r.Header.Get("Authorization"); got != "Bearer installation-111" {
				t.Errorf("issue authorization = %q", got)
			}
			var body struct {
				Body string `json:"body"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode issue body: %v", err)
			}
			issueBody = body.Body
			_, _ = w.Write([]byte(`{"id":9001,"number":42,"html_url":"https://github.com/Acme/Repo/issues/42"}`))
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()

	manager := newRunnerGitHubTestManager(t, server.URL)
	rpc.WithGitHubActions(&Service{repo: q, github: manager})
	insertGitHubRPCTask(t, q, tdb, "task_github_success", "Acme/Repo", 0)
	activateGitHubRPCTask(t, q, "task_github_success", "runner_1", time.Now().Add(time.Minute))

	resp, err := rpc.GitHubCreateIssue(context.Background(), connect.NewRequest(&runnerv1.GitHubCreateIssueRequest{
		TaskId:      "task_github_success",
		ExecutionId: "exec_task_github_success",
		RunnerId:    "runner_1",
		ClaimId:     "claim_task_github_success",
		Repo:        "acme/repo",
		Title:       "Secure operation",
		Body:        "Created by the active runner.",
	}))
	if err != nil {
		t.Fatalf("GitHubCreateIssue: %v", err)
	}
	if resp.Msg.Number != 42 || resp.Msg.Url != "https://github.com/Acme/Repo/issues/42" {
		t.Fatalf("GitHubCreateIssue response = %+v", resp.Msg)
	}
	if calls.Load() != 3 {
		t.Fatalf("GitHub API calls = %d, want installation discovery, token, and issue", calls.Load())
	}
	if !strings.Contains(issueBody, "Task: task_github_success") || !strings.Contains(issueBody, "Attempt: exec_task_github_success") {
		t.Fatalf("issue body missing Chetter signature: %q", issueBody)
	}

	task, err := q.GetTaskByID(context.Background(), "task_github_success")
	if err != nil {
		t.Fatalf("get pinned task: %v", err)
	}
	if !task.GithubInstallationID.Valid || task.GithubInstallationID.Int64 != 111 {
		t.Fatalf("pinned installation = %+v, want 111", task.GithubInstallationID)
	}
	var artifactCount, auditCount int
	if err := tdb.DB.QueryRow(testQuery(tdb.Dialect(),
		"SELECT COUNT(*) FROM task_artifacts WHERE task_id = ? AND execution_attempt_id = ? AND repo = ?",
		"SELECT COUNT(*) FROM task_artifacts WHERE task_id = $1 AND execution_attempt_id = $2 AND repo = $3"),
		"task_github_success", "exec_task_github_success", "Acme/Repo").Scan(&artifactCount); err != nil {
		t.Fatalf("count recorded artifact: %v", err)
	}
	if err := tdb.DB.QueryRow(testQuery(tdb.Dialect(),
		"SELECT COUNT(*) FROM audit_log WHERE source_id = ? AND detail LIKE ?",
		"SELECT COUNT(*) FROM audit_log WHERE source_id = $1 AND detail LIKE $2"),
		"task_github_success", "%installation 111%").Scan(&auditCount); err != nil {
		t.Fatalf("count audit event: %v", err)
	}
	if artifactCount != 1 || auditCount != 1 {
		t.Fatalf("recorded artifact/audit counts = %d/%d, want 1/1", artifactCount, auditCount)
	}
}

func TestGitHubCreateIssueRejectsUnauthorizedExecutionBeforeAPICall(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "authorization failure reached GitHub", http.StatusInternalServerError)
	}))
	defer server.Close()
	manager := newRunnerGitHubTestManager(t, server.URL)

	tests := []struct {
		name     string
		wantCode connect.Code
		setup    func(*testing.T, data.Repository, *testdb.TestDB)
		request  *runnerv1.GitHubCreateIssueRequest
	}{
		{
			name:     "wrong repository",
			wantCode: connect.CodePermissionDenied,
			setup: func(t *testing.T, q data.Repository, tdb *testdb.TestDB) {
				insertGitHubRPCTask(t, q, tdb, "task_wrong_repo", "Acme/Repo", 111)
				activateGitHubRPCTask(t, q, "task_wrong_repo", "runner_1", time.Now().Add(time.Minute))
			},
			request: &runnerv1.GitHubCreateIssueRequest{TaskId: "task_wrong_repo", ExecutionId: "exec_task_wrong_repo", RunnerId: "runner_1", ClaimId: "claim_task_wrong_repo", Repo: "Acme/Other", Title: "denied"},
		},
		{
			name:     "wrong runner",
			wantCode: connect.CodePermissionDenied,
			setup: func(t *testing.T, q data.Repository, tdb *testdb.TestDB) {
				insertGitHubRPCTask(t, q, tdb, "task_wrong_runner", "Acme/Repo", 111)
				activateGitHubRPCTask(t, q, "task_wrong_runner", "runner_owner", time.Now().Add(time.Minute))
			},
			request: &runnerv1.GitHubCreateIssueRequest{TaskId: "task_wrong_runner", ExecutionId: "exec_task_wrong_runner", RunnerId: "runner_other", ClaimId: "claim_task_wrong_runner", Repo: "Acme/Repo", Title: "denied"},
		},
		{
			name:     "attempt is not running",
			wantCode: connect.CodeFailedPrecondition,
			setup: func(t *testing.T, q data.Repository, tdb *testdb.TestDB) {
				insertGitHubRPCTask(t, q, tdb, "task_stale_attempt", "Acme/Repo", 111)
				markTaskRunning(t, q, "task_stale_attempt", time.Now().UTC())
			},
			request: &runnerv1.GitHubCreateIssueRequest{TaskId: "task_stale_attempt", ExecutionId: "exec_task_stale_attempt", RunnerId: "runner_1", ClaimId: "claim_task_stale_attempt", Repo: "Acme/Repo", Title: "denied"},
		},
		{
			name:     "task is not running",
			wantCode: connect.CodeFailedPrecondition,
			setup: func(t *testing.T, q data.Repository, tdb *testdb.TestDB) {
				insertGitHubRPCTask(t, q, tdb, "task_stale_task", "Acme/Repo", 111)
				now := time.Now().UTC()
				markPendingExecutionAttemptClaimed(t, q, "task_stale_task", "runner_1", now, now.Add(time.Minute))
			},
			request: &runnerv1.GitHubCreateIssueRequest{TaskId: "task_stale_task", ExecutionId: "exec_task_stale_task", RunnerId: "runner_1", ClaimId: "claim_task_stale_task", Repo: "Acme/Repo", Title: "denied"},
		},
		{
			name:     "lease is expired",
			wantCode: connect.CodeFailedPrecondition,
			setup: func(t *testing.T, q data.Repository, tdb *testdb.TestDB) {
				insertGitHubRPCTask(t, q, tdb, "task_expired_lease", "Acme/Repo", 111)
				activateGitHubRPCTask(t, q, "task_expired_lease", "runner_1", time.Now().Add(-time.Second))
			},
			request: &runnerv1.GitHubCreateIssueRequest{TaskId: "task_expired_lease", ExecutionId: "exec_task_expired_lease", RunnerId: "runner_1", ClaimId: "claim_task_expired_lease", Repo: "Acme/Repo", Title: "denied"},
		},
		{
			name:     "execution belongs to another task",
			wantCode: connect.CodePermissionDenied,
			setup: func(t *testing.T, q data.Repository, tdb *testdb.TestDB) {
				insertGitHubRPCTask(t, q, tdb, "task_execution_owner", "Acme/Repo", 111)
				activateGitHubRPCTask(t, q, "task_execution_owner", "runner_1", time.Now().Add(time.Minute))
				insertGitHubRPCTask(t, q, tdb, "task_request_owner", "Acme/Repo", 111)
			},
			request: &runnerv1.GitHubCreateIssueRequest{TaskId: "task_request_owner", ExecutionId: "exec_task_execution_owner", RunnerId: "runner_1", ClaimId: "claim_task_execution_owner", Repo: "Acme/Repo", Title: "denied"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rpc, q, tdb, cleanup := newRPCTestService(t)
			defer cleanup()
			rpc.WithGitHubActions(&Service{repo: q, github: manager})
			tt.setup(t, q, tdb)
			before := calls.Load()

			_, err := rpc.GitHubCreateIssue(context.Background(), connect.NewRequest(tt.request))
			if got := connect.CodeOf(err); got != tt.wantCode {
				t.Fatalf("GitHubCreateIssue code = %s, want %s (err=%v)", got, tt.wantCode, err)
			}
			if got := calls.Load(); got != before {
				t.Fatalf("GitHub API calls changed from %d to %d on authorization failure", before, got)
			}
		})
	}
}

func TestGetGitHubCredentialReturnsRestrictedActiveExecutionToken(t *testing.T) {
	rpc, q, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/app/installations/111/access_tokens" {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		var payload struct {
			Repositories []string          `json:"repositories"`
			Permissions  map[string]string `json:"permissions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode credential payload: %v", err)
		}
		if len(payload.Repositories) != 1 || payload.Repositories[0] != "Repo" || payload.Permissions["contents"] != "write" || payload.Permissions["issues"] != "read" || payload.Permissions["pull_requests"] != "read" {
			t.Errorf("unexpected credential payload: %+v", payload)
		}
		writeRunnerCredentialResponse(w, "task-git-token", time.Now().Add(time.Hour))
	}))
	defer server.Close()
	rpc.WithGitHubActions(&Service{repo: q, github: newRunnerGitHubTestManager(t, server.URL)})
	insertGitHubRPCTask(t, q, tdb, "task_credential_success", "Acme/Repo", 111)
	activateGitHubRPCTask(t, q, "task_credential_success", "runner_1", time.Now().Add(time.Minute))

	resp, err := rpc.GetGitHubCredential(context.Background(), connect.NewRequest(&runnerv1.GetGitHubCredentialRequest{
		RunnerId: "runner_1", TaskId: "task_credential_success", ExecutionId: "exec_task_credential_success", ClaimId: "claim_task_credential_success", Repo: "acme/repo",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Msg.Username != "x-access-token" || resp.Msg.Token != "task-git-token" {
		t.Fatalf("response = %+v", resp.Msg)
	}
	if _, err := time.Parse(time.RFC3339Nano, resp.Msg.ExpiresAt); err != nil {
		t.Fatalf("expires_at = %q: %v", resp.Msg.ExpiresAt, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("GitHub calls = %d, want 1", calls.Load())
	}
}

func TestGetGitHubCredentialRejectsUnauthorizedExecutionBeforeExchange(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "must not be called", http.StatusInternalServerError)
	}))
	defer server.Close()
	manager := newRunnerGitHubTestManager(t, server.URL)
	tests := []struct {
		name        string
		requestRepo string
		runnerID    string
		lease       time.Time
		wantCode    connect.Code
	}{
		{name: "wrong repo", requestRepo: "Acme/Other", runnerID: "runner_1", lease: time.Now().Add(time.Minute), wantCode: connect.CodePermissionDenied},
		{name: "wrong runner", requestRepo: "Acme/Repo", runnerID: "runner_other", lease: time.Now().Add(time.Minute), wantCode: connect.CodePermissionDenied},
		{name: "expired lease", requestRepo: "Acme/Repo", runnerID: "runner_1", lease: time.Now().Add(-time.Second), wantCode: connect.CodeFailedPrecondition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rpc, q, tdb, cleanup := newRPCTestService(t)
			defer cleanup()
			rpc.WithGitHubActions(&Service{repo: q, github: manager})
			taskID := "task_credential_" + strings.ReplaceAll(tt.name, " ", "_")
			insertGitHubRPCTask(t, q, tdb, taskID, "Acme/Repo", 111)
			activateGitHubRPCTask(t, q, taskID, "runner_1", tt.lease)
			before := calls.Load()
			_, err := rpc.GetGitHubCredential(context.Background(), connect.NewRequest(&runnerv1.GetGitHubCredentialRequest{
				RunnerId: tt.runnerID, TaskId: taskID, ExecutionId: "exec_" + taskID, ClaimId: "claim_" + taskID, Repo: tt.requestRepo,
			}))
			if connect.CodeOf(err) != tt.wantCode {
				t.Fatalf("code = %s, want %s: %v", connect.CodeOf(err), tt.wantCode, err)
			}
			if calls.Load() != before {
				t.Fatal("credential exchange occurred before authorization")
			}
		})
	}
}

func TestGetGitHubCredentialRechecksFenceAfterExchange(t *testing.T) {
	rpc, q, tdb, cleanup := newRPCTestService(t)
	defer cleanup()
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		writeRunnerCredentialResponse(w, "must-not-be-returned", time.Now().Add(time.Hour))
	}))
	defer server.Close()
	rpc.WithGitHubActions(&Service{repo: q, github: newRunnerGitHubTestManager(t, server.URL)})
	insertGitHubRPCTask(t, q, tdb, "task_credential_race", "Acme/Repo", 111)
	activateGitHubRPCTask(t, q, "task_credential_race", "runner_1", time.Now().Add(time.Minute))

	errCh := make(chan error, 1)
	go func() {
		_, err := rpc.GetGitHubCredential(context.Background(), connect.NewRequest(&runnerv1.GetGitHubCredentialRequest{
			RunnerId: "runner_1", TaskId: "task_credential_race", ExecutionId: "exec_task_credential_race", ClaimId: "claim_task_credential_race", Repo: "Acme/Repo",
		}))
		errCh <- err
	}()
	<-started
	if _, err := tdb.DB.Exec(testQuery(tdb.Dialect(),
		"UPDATE execution_attempts SET lease_expires_at = ? WHERE id = ?",
		"UPDATE execution_attempts SET lease_expires_at = $1 WHERE id = $2"), time.Now().Add(-time.Minute), "exec_task_credential_race"); err != nil {
		t.Fatalf("expire lease: %v", err)
	}
	close(release)
	if err := <-errCh; connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %s, want failed_precondition: %v", connect.CodeOf(err), err)
	}
}

func writeRunnerCredentialResponse(w http.ResponseWriter, token string, expiresAt time.Time) {
	w.WriteHeader(http.StatusCreated)
	_, _ = fmt.Fprintf(w, `{"token":%q,"expires_at":%q}`, token, expiresAt.UTC().Format(time.RFC3339Nano))
}
