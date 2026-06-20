package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	apiv1 "github.com/flatout-works/chetter/gen/proto/api/v1"
	"github.com/flatout-works/chetter/gen/proto/api/v1/apiv1connect"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(apiv1connect.NewAdminServiceHandler(&testAdminService{t: t}))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}))
}

type testAdminService struct {
	t *testing.T
}

func (s *testAdminService) CreateToken(_ context.Context, req *connect.Request[apiv1.CreateTokenRequest]) (*connect.Response[apiv1.CreateTokenResponse], error) {
	if req.Msg.TeamName != "platform" || req.Msg.UserName != "alice" || req.Msg.TokenName != "alice-cli" {
		s.t.Fatalf("CreateToken request = %+v", req.Msg)
	}
	return connect.NewResponse(&apiv1.CreateTokenResponse{
		Token:    "new-token-value",
		TeamName: req.Msg.TeamName,
		UserName: req.Msg.UserName,
	}), nil
}

func (s *testAdminService) ListTokens(context.Context, *connect.Request[apiv1.ListTokensRequest]) (*connect.Response[apiv1.ListTokensResponse], error) {
	return connect.NewResponse(&apiv1.ListTokensResponse{Tokens: []*apiv1.TokenInfo{{
		Name:     "token-1",
		UserName: "alice",
		TeamName: "platform",
	}}}), nil
}

func (s *testAdminService) DeleteToken(_ context.Context, req *connect.Request[apiv1.DeleteTokenRequest]) (*connect.Response[apiv1.DeleteTokenResponse], error) {
	if req.Msg.Name == "error" {
		return nil, connect.NewError(connect.CodeInternal, nil)
	}
	if req.Msg.Name != "token-1" {
		s.t.Fatalf("DeleteToken name = %q", req.Msg.Name)
	}
	return connect.NewResponse(&apiv1.DeleteTokenResponse{Deleted: true}), nil
}

func (s *testAdminService) CreateTeam(context.Context, *connect.Request[apiv1.CreateTeamRequest]) (*connect.Response[apiv1.CreateTeamResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (s *testAdminService) ListTeams(context.Context, *connect.Request[apiv1.ListTeamsRequest]) (*connect.Response[apiv1.ListTeamsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (s *testAdminService) DeleteTeam(context.Context, *connect.Request[apiv1.DeleteTeamRequest]) (*connect.Response[apiv1.DeleteTeamResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (s *testAdminService) ListUsers(context.Context, *connect.Request[apiv1.ListUsersRequest]) (*connect.Response[apiv1.ListUsersResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (s *testAdminService) ListAuditEvents(context.Context, *connect.Request[apiv1.ListAuditEventsRequest]) (*connect.Response[apiv1.ListAuditEventsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func (s *testAdminService) ListTaskArtifacts(context.Context, *connect.Request[apiv1.ListTaskArtifactsRequest]) (*connect.Response[apiv1.ListTaskArtifactsResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func TestTokenCreate(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	err := tokenCmd([]string{
		"create",
		"--server", srv.URL,
		"--token", "test-token",
		"--team", "platform",
		"--user", "alice",
		"--name", "alice-cli",
	}, "", "")
	if err != nil {
		t.Fatalf("tokenCmd create: %v", err)
	}
}

func TestTokenList(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	err := tokenCmd([]string{
		"list",
		"--server", srv.URL,
		"--token", "test-token",
	}, "", "")
	if err != nil {
		t.Fatalf("tokenCmd list: %v", err)
	}
}

func TestTokenDelete(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	err := tokenCmd([]string{
		"delete",
		"--server", srv.URL,
		"--token", "test-token",
		"--name", "token-1",
	}, "", "")
	if err != nil {
		t.Fatalf("tokenCmd delete: %v", err)
	}
}

func TestTokenCreateMissingRequiredFlags(t *testing.T) {
	err := tokenCmd([]string{"create", "--server", "http://x", "--token", "t"}, "", "")
	if err == nil {
		t.Fatal("expected error for missing flags, got nil")
	}
	if !strings.Contains(err.Error(), "--team") {
		t.Errorf("error = %v, want mention of --team", err)
	}
}

func TestTokenCmdMissingServer(t *testing.T) {
	err := tokenCmd([]string{"create", "--server", "", "--token", "t", "--team", "a", "--user", "b", "--name", "c"}, "", "t")
	if err == nil {
		t.Fatal("expected error for empty server, got nil")
	}
	if !strings.Contains(err.Error(), "--server") {
		t.Errorf("error = %v, want mention of --server", err)
	}
}

func TestTokenCreateMissingToken(t *testing.T) {
	err := tokenCmd([]string{"create", "--server", "http://srv", "--team", "a", "--user", "b", "--name", "c"}, "http://srv", "")
	if err == nil {
		t.Fatal("expected error for empty token, got nil")
	}
	if !strings.Contains(err.Error(), "--token") {
		t.Errorf("error = %v, want mention of --token", err)
	}
}

func TestTokenCmdDefaultsFromEnv(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	err := tokenCmd([]string{
		"list",
		"--server", srv.URL,
	}, srv.URL, "test-token")
	if err != nil {
		t.Fatalf("tokenCmd list with defaults: %v", err)
	}
}

func TestTokenDeleteServerError(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	err := tokenCmd([]string{
		"delete",
		"--server", srv.URL,
		"--token", "test-token",
		"--name", "error",
	}, "", "")
	if err == nil {
		t.Fatal("expected error for internal response, got nil")
	}
	if !strings.Contains(err.Error(), "internal") {
		t.Errorf("error = %v, want internal", err)
	}
}

func TestTokenCmdUnauthorized(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	err := tokenCmd([]string{
		"list",
		"--server", srv.URL,
		"--token", "bad-token",
	}, "", "")
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if !strings.Contains(err.Error(), "unauthenticated") && !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want unauthenticated or 401", err)
	}
}

func TestPrintProtoJSON(t *testing.T) {
	printProtoJSON(&apiv1.DeleteTokenResponse{Deleted: true})
}

func TestTokenCmdUnknownSubcommand(t *testing.T) {
	err := tokenCmd([]string{"unknown"}, "http://srv", "tok")
	if err != nil {
		t.Fatalf("expected nil for unknown subcommand, got %v", err)
	}
}
