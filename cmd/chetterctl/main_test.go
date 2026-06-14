package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/v1/tokens":
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]string{
				"token": "new-token-value",
				"name":  body["token_name"],
			})

		case r.Method == "GET" && r.URL.Path == "/api/v1/tokens":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]string{
				{"name": "token-1", "user_name": "alice", "team_name": "platform"},
			})

		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/v1/tokens/"):
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

		case r.URL.Path == "/api/v1/tokens/error":
			http.Error(w, `{"error":"internal"}`, http.StatusInternalServerError)

		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
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
	err := tokenCmd([]string{"create", "--token", "t", "--team", "a", "--user", "b", "--name", "c"}, "", "t")
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

func TestApiGet(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body, err := apiGet(srv.URL, "test-token", "/api/v1/tokens")
	if err != nil {
		t.Fatalf("apiGet: %v", err)
	}
	if !strings.Contains(string(body), "token-1") {
		t.Errorf("response = %s, want token-1", string(body))
	}
}

func TestApiPost(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	reqBody, _ := json.Marshal(map[string]string{"token_name": "cli"})
	body, err := apiPost(srv.URL, "test-token", "/api/v1/tokens", reqBody)
	if err != nil {
		t.Fatalf("apiPost: %v", err)
	}
	if !strings.Contains(string(body), "new-token-value") {
		t.Errorf("response = %s, want new-token-value", string(body))
	}
}

func TestApiDelete(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	body, err := apiDelete(srv.URL, "test-token", "/api/v1/tokens/token-1")
	if err != nil {
		t.Fatalf("apiDelete: %v", err)
	}
	if !strings.Contains(string(body), "deleted") {
		t.Errorf("response = %s, want deleted", string(body))
	}
}

func TestApiGetServerError(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	_, err := apiGet(srv.URL, "test-token", "/api/v1/tokens/error")
	if err == nil {
		t.Fatal("expected error for 500, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %v, want 500", err)
	}
}

func TestApiPostUnauthorized(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	_, err := apiPost(srv.URL, "bad-token", "/api/v1/tokens", nil)
	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want 401", err)
	}
}

func TestPrintJSONValid(t *testing.T) {
	data := []byte(`{"a":1,"b":2}`)
	printJSON(data)
}

func TestPrintJSONInvalid(t *testing.T) {
	data := []byte(`not-json`)
	printJSON(data)
}

func TestTokenCmdUnknownSubcommand(t *testing.T) {
	err := tokenCmd([]string{"unknown"}, "http://srv", "tok")
	if err != nil {
		t.Fatalf("expected nil for unknown subcommand, got %v", err)
	}
}
