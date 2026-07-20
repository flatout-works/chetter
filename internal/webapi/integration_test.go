package webapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/flatout-works/chetter/gen/proto/api/v1"
	apiv1connect "github.com/flatout-works/chetter/gen/proto/api/v1/apiv1connect"
	"github.com/flatout-works/chetter/internal/config"
	"github.com/flatout-works/chetter/internal/service"
	"github.com/flatout-works/chetter/internal/store"
	"github.com/flatout-works/chetter/internal/testdb"
)

const webAPITestAdminToken = "webapi-test-admin-token"

var webAPITestDB *testdb.PackageDB

func TestMain(m *testing.M) {
	webAPITestDB = testdb.StartPackageDB(m)
	if webAPITestDB == nil {
		os.Exit(0)
	}
	code := m.Run()
	webAPITestDB.Close()
	os.Exit(code)
}

func TestWebAPIRejectsMissingAuth(t *testing.T) {
	server, cleanup := newWebAPITestServer(t)
	defer cleanup()

	client := apiv1connect.NewTaskServiceClient(server.Client(), server.URL)
	_, err := client.ListTasks(context.Background(), connect.NewRequest(&apiv1.ListTasksRequest{Limit: 10}))
	if err == nil {
		t.Fatal("expected unauthenticated error")
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %s, want %s: %v", connect.CodeOf(err), connect.CodeUnauthenticated, err)
	}
}

func TestWebAPISubmitGetAndCancelTask(t *testing.T) {
	server, cleanup := newWebAPITestServer(t)
	defer cleanup()

	tasks := apiv1connect.NewTaskServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)

	submitted, err := tasks.SubmitTask(context.Background(), connect.NewRequest(&apiv1.SubmitTaskRequest{
		Prompt: "web api integration task",
	}))
	if err != nil {
		t.Fatalf("SubmitTask: %v", err)
	}
	if submitted.Msg.Task == nil {
		t.Fatal("SubmitTask returned nil task")
	}
	if submitted.Msg.Task.AgentImage != "runner:latest" {
		t.Fatalf("agent image = %q, want runner:latest", submitted.Msg.Task.AgentImage)
	}

	got, err := tasks.GetTask(context.Background(), connect.NewRequest(&apiv1.GetTaskRequest{TaskId: submitted.Msg.Task.Id}))
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.Msg.Task.GetPrompt() != "web api integration task" {
		t.Fatalf("prompt = %q", got.Msg.Task.GetPrompt())
	}

	extended, err := tasks.ExtendTask(context.Background(), connect.NewRequest(&apiv1.ExtendTaskRequest{
		TaskId:       submitted.Msg.Task.Id,
		ExtensionSec: 300,
	}))
	if err != nil {
		t.Fatalf("ExtendTask: %v", err)
	}
	if extended.Msg.Task.GetTimeoutSec() != 900 {
		t.Fatalf("timeout = %d, want 900", extended.Msg.Task.GetTimeoutSec())
	}

	cancelled, err := tasks.CancelTask(context.Background(), connect.NewRequest(&apiv1.CancelTaskRequest{
		TaskId: submitted.Msg.Task.Id,
		Reason: "integration cancel",
	}))
	if err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	if cancelled.Msg.Task.GetStatus() != "cancelled" {
		t.Fatalf("status = %q, want cancelled", cancelled.Msg.Task.GetStatus())
	}
	if cancelled.Msg.Task.GetError() != "integration cancel" {
		t.Fatalf("error = %q, want integration cancel", cancelled.Msg.Task.GetError())
	}

	_, err = tasks.ExtendTask(context.Background(), connect.NewRequest(&apiv1.ExtendTaskRequest{
		TaskId:       submitted.Msg.Task.Id,
		ExtensionSec: 300,
	}))
	if err == nil {
		t.Fatal("expected ExtendTask to reject a cancelled task")
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("ExtendTask error code = %s, want %s", connect.CodeOf(err), connect.CodeFailedPrecondition)
	}
}

func TestWebAPITeamTokenScopesTasks(t *testing.T) {
	server, cleanup := newWebAPITestServer(t)
	defer cleanup()

	admin := apiv1connect.NewAdminServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)
	teamA, err := admin.CreateToken(context.Background(), connect.NewRequest(&apiv1.CreateTokenRequest{
		TeamNames: []string{"team-a"},
		UserName:  "alice",
		TokenName: "alice-token",
	}))
	if err != nil {
		t.Fatalf("CreateToken team-a: %v", err)
	}
	teamB, err := admin.CreateToken(context.Background(), connect.NewRequest(&apiv1.CreateTokenRequest{
		TeamNames: []string{"team-b"},
		UserName:  "bob",
		TokenName: "bob-token",
	}))
	if err != nil {
		t.Fatalf("CreateToken team-b: %v", err)
	}

	tasksA := apiv1connect.NewTaskServiceClient(authHTTPClient(server, teamA.Msg.Token), server.URL)
	tasksB := apiv1connect.NewTaskServiceClient(authHTTPClient(server, teamB.Msg.Token), server.URL)
	if _, err := tasksA.SubmitTask(context.Background(), connect.NewRequest(&apiv1.SubmitTaskRequest{Prompt: "team-a task"})); err != nil {
		t.Fatalf("SubmitTask team-a: %v", err)
	}
	if _, err := tasksB.SubmitTask(context.Background(), connect.NewRequest(&apiv1.SubmitTaskRequest{Prompt: "team-b task"})); err != nil {
		t.Fatalf("SubmitTask team-b: %v", err)
	}

	listedA, err := tasksA.ListTasks(context.Background(), connect.NewRequest(&apiv1.ListTasksRequest{Limit: 10}))
	if err != nil {
		t.Fatalf("ListTasks team-a: %v", err)
	}
	if len(listedA.Msg.Tasks) != 1 || listedA.Msg.Tasks[0].GetPrompt() != "team-a task" {
		t.Fatalf("team-a saw wrong tasks: %+v", listedA.Msg.Tasks)
	}

	listedB, err := tasksB.ListTasks(context.Background(), connect.NewRequest(&apiv1.ListTasksRequest{Limit: 10}))
	if err != nil {
		t.Fatalf("ListTasks team-b: %v", err)
	}
	if len(listedB.Msg.Tasks) != 1 || listedB.Msg.Tasks[0].GetPrompt() != "team-b task" {
		t.Fatalf("team-b saw wrong tasks: %+v", listedB.Msg.Tasks)
	}

	adminTasks := apiv1connect.NewTaskServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)
	listedAdmin, err := adminTasks.ListTasks(context.Background(), connect.NewRequest(&apiv1.ListTasksRequest{Limit: 10}))
	if err != nil {
		t.Fatalf("ListTasks admin: %v", err)
	}
	if len(listedAdmin.Msg.Tasks) != 2 {
		t.Fatalf("admin saw %d tasks, want 2", len(listedAdmin.Msg.Tasks))
	}

	_, err = admin.ListTeams(context.Background(), connect.NewRequest(&apiv1.ListTeamsRequest{}))
	if err != nil {
		t.Fatalf("admin ListTeams: %v", err)
	}
	teamAdmin := apiv1connect.NewAdminServiceClient(authHTTPClient(server, teamA.Msg.Token), server.URL)
	if _, err := teamAdmin.ListTeams(context.Background(), connect.NewRequest(&apiv1.ListTeamsRequest{})); err == nil {
		t.Fatal("team token should not be allowed to list teams")
	}
}

func TestWebAPITriggerRunHistory(t *testing.T) {
	server, cleanup := newWebAPITestServer(t)
	defer cleanup()

	triggers := apiv1connect.NewTriggerServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)
	created, err := triggers.CreateTrigger(context.Background(), connect.NewRequest(&apiv1.CreateTriggerRequest{
		Name:        "hourly-smoke",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "run from trigger",
	}))
	if err != nil {
		t.Fatalf("CreateTrigger: %v", err)
	}
	if created.Msg.Trigger.GetAgentImage() != "runner:latest" {
		t.Fatalf("agent image = %q, want runner:latest", created.Msg.Trigger.GetAgentImage())
	}

	run, err := triggers.RunTrigger(context.Background(), connect.NewRequest(&apiv1.RunTriggerRequest{Name: "hourly-smoke"}))
	if err != nil {
		t.Fatalf("RunTrigger: %v", err)
	}
	if run.Msg.Task.GetPrompt() != "run from trigger" {
		t.Fatalf("run task prompt = %q", run.Msg.Task.GetPrompt())
	}

	runs, err := triggers.ListTriggerRuns(context.Background(), connect.NewRequest(&apiv1.ListTriggerRunsRequest{TriggerName: "hourly-smoke", Limit: 10}))
	if err != nil {
		t.Fatalf("ListTriggerRuns: %v", err)
	}
	if len(runs.Msg.Runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs.Msg.Runs))
	}
	if runs.Msg.Runs[0].GetTaskId() != run.Msg.Task.GetId() {
		t.Fatalf("run task id = %q, want %q", runs.Msg.Runs[0].GetTaskId(), run.Msg.Task.GetId())
	}
	if runs.Msg.Runs[0].GetStatus() != "submitted" {
		t.Fatalf("run status = %q, want submitted", runs.Msg.Runs[0].GetStatus())
	}
}

func TestWebAPITeamTokenCannotMutateOtherTeamTrigger(t *testing.T) {
	server, cleanup := newWebAPITestServer(t)
	defer cleanup()

	admin := apiv1connect.NewAdminServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)
	teamA, err := admin.CreateToken(context.Background(), connect.NewRequest(&apiv1.CreateTokenRequest{
		TeamNames: []string{"trigger-team-a"},
		UserName:  "alice",
		TokenName: "trigger-alice-token",
	}))
	if err != nil {
		t.Fatalf("CreateToken team-a: %v", err)
	}
	teamB, err := admin.CreateToken(context.Background(), connect.NewRequest(&apiv1.CreateTokenRequest{
		TeamNames: []string{"trigger-team-b"},
		UserName:  "bob",
		TokenName: "trigger-bob-token",
	}))
	if err != nil {
		t.Fatalf("CreateToken team-b: %v", err)
	}

	triggersA := apiv1connect.NewTriggerServiceClient(authHTTPClient(server, teamA.Msg.Token), server.URL)
	if _, err := triggersA.CreateTrigger(context.Background(), connect.NewRequest(&apiv1.CreateTriggerRequest{
		Name:        "team-a-trigger",
		TriggerType: store.TriggerTypeCron,
		CronExpr:    "@hourly",
		Prompt:      "team-a trigger task",
	})); err != nil {
		t.Fatalf("CreateTrigger team-a: %v", err)
	}

	triggersB := apiv1connect.NewTriggerServiceClient(authHTTPClient(server, teamB.Msg.Token), server.URL)
	if _, err := triggersB.UpdateTrigger(context.Background(), connect.NewRequest(&apiv1.UpdateTriggerRequest{
		Name:   "team-a-trigger",
		Prompt: "team-b takeover",
	})); err == nil {
		t.Fatal("team-b UpdateTrigger should fail")
	}
	if _, err := triggersB.RunTrigger(context.Background(), connect.NewRequest(&apiv1.RunTriggerRequest{Name: "team-a-trigger"})); err == nil {
		t.Fatal("team-b RunTrigger should fail")
	}
	if _, err := triggersB.DeleteTrigger(context.Background(), connect.NewRequest(&apiv1.DeleteTriggerRequest{Name: "team-a-trigger"})); err == nil {
		t.Fatal("team-b DeleteTrigger should fail")
	}

	listed, err := triggersA.ListTriggers(context.Background(), connect.NewRequest(&apiv1.ListTriggersRequest{}))
	if err != nil {
		t.Fatalf("ListTriggers team-a: %v", err)
	}
	if len(listed.Msg.Triggers) != 1 || listed.Msg.Triggers[0].GetPrompt() != "team-a trigger task" {
		t.Fatalf("team-a trigger was modified/deleted: %+v", listed.Msg.Triggers)
	}
}

func TestWebAPITeamTokenCannotSubscribeOtherTeamTaskEvents(t *testing.T) {
	server, cleanup := newWebAPITestServer(t)
	defer cleanup()

	admin := apiv1connect.NewAdminServiceClient(authHTTPClient(server, webAPITestAdminToken), server.URL)
	teamA, err := admin.CreateToken(context.Background(), connect.NewRequest(&apiv1.CreateTokenRequest{
		TeamNames: []string{"events-team-a"},
		UserName:  "alice",
		TokenName: "events-alice-token",
	}))
	if err != nil {
		t.Fatalf("CreateToken team-a: %v", err)
	}
	teamB, err := admin.CreateToken(context.Background(), connect.NewRequest(&apiv1.CreateTokenRequest{
		TeamNames: []string{"events-team-b"},
		UserName:  "bob",
		TokenName: "events-bob-token",
	}))
	if err != nil {
		t.Fatalf("CreateToken team-b: %v", err)
	}

	tasksA := apiv1connect.NewTaskServiceClient(authHTTPClient(server, teamA.Msg.Token), server.URL)
	submitted, err := tasksA.SubmitTask(context.Background(), connect.NewRequest(&apiv1.SubmitTaskRequest{Prompt: "team-a stream task"}))
	if err != nil {
		t.Fatalf("SubmitTask team-a: %v", err)
	}

	tasksB := apiv1connect.NewTaskServiceClient(authHTTPClient(server, teamB.Msg.Token), server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stream, err := tasksB.SubscribeTaskEvents(ctx, connect.NewRequest(&apiv1.SubscribeTaskEventsRequest{
		TaskId: submitted.Msg.Task.GetId(),
		Since:  time.Unix(0, 0).UTC().Format(time.RFC3339),
	}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return
		}
		t.Fatalf("SubscribeTaskEvents returned wrong error: %v", err)
	}
	if stream.Receive() {
		t.Fatalf("team-b received event for team-a task: %+v", stream.Msg())
	}
	if err := stream.Err(); err == nil || connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("stream error = %v, want not_found", err)
	}
}

type authRoundTripper struct {
	base  http.RoundTripper
	token string
}

func (a authRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+a.token)
	return a.base.RoundTrip(clone)
}

func authHTTPClient(server *httptest.Server, token string) *http.Client {
	client := server.Client()
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{
		Transport: authRoundTripper{base: base, token: token},
		Timeout:   10 * time.Second,
	}
}

func newWebAPITestServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	tdb, cleanupDB := webAPITestDB.NewTestDB(t)
	cfg := config.Config{DefaultAgentImage: "runner:latest", DefaultTaskTimeoutSec: 600}
	st, err := store.Open(tdb.DSN, tdb.Dialect())
	if err != nil {
		cleanupDB()
		t.Fatalf("store.Open: %v", err)
	}
	svc := service.New(cfg, st)
	bus := NewEventBus()
	mux := http.NewServeMux()
	RegisterHandlers(mux, NewHandlers(svc, bus), webAPITestAdminToken, st.DB())
	server := httptest.NewServer(mux)
	return server, func() {
		server.Close()
		bus.CloseAll()
		_ = st.Close()
		cleanupDB()
	}
}
