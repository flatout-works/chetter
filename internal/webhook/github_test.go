package webhook

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
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newTestManager(t *testing.T, apiBase string) *Manager {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
	opts := []ManagerOption{}
	if apiBase != "" {
		opts = append(opts, WithAPIBaseURL(apiBase))
	}
	manager, err := NewManager(123, encoded, opts...)
	if err != nil {
		t.Fatalf("create GitHub manager: %v", err)
	}
	return manager
}

func writeTokenResponse(w http.ResponseWriter, token string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = fmt.Fprintf(w, `{"token":%q,"expires_at":%q}`, token, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
}

func TestManagerAppLoginUsesAppJWTAndCachesSuccess(t *testing.T) {
	var calls int
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		authorization = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"slug":"chetterbot"}`))
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL)

	login, err := manager.AppLogin(context.Background())
	if err != nil {
		t.Fatalf("AppLogin returned error: %v", err)
	}
	if login != "chetterbot[bot]" {
		t.Fatalf("AppLogin = %q, want chetterbot[bot]", login)
	}
	if !strings.HasPrefix(authorization, "Bearer ") || strings.Count(strings.TrimPrefix(authorization, "Bearer "), ".") != 2 {
		t.Fatalf("expected JWT authorization, got %q", authorization)
	}
	login, err = manager.AppLogin(context.Background())
	if err != nil || login != "chetterbot[bot]" || calls != 1 {
		t.Fatalf("cached AppLogin = %q, err=%v, calls=%d", login, err, calls)
	}
}

func TestManagerInstallationTokenCachesAreIsolated(t *testing.T) {
	var mu sync.Mutex
	tokenCalls := map[string]int{}
	authorizations := map[string][]string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/app/installations/111/access_tokens":
			tokenCalls["111"]++
			writeTokenResponse(w, "token-111")
		case "/app/installations/222/access_tokens":
			tokenCalls["222"]++
			writeTokenResponse(w, "token-222")
		case "/repos/acme/one/issues/1/labels", "/repos/acme/two/issues/1/labels":
			authorizations[r.URL.Path] = append(authorizations[r.URL.Path], r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`[]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL)

	client111, err := manager.ClientForInstallation(context.Background(), 111)
	if err != nil {
		t.Fatal(err)
	}
	client222, err := manager.ClientForInstallation(context.Background(), 222)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := client111.HasLabel(context.Background(), "acme/one", 1, "x"); err != nil {
			t.Fatal(err)
		}
		if _, err := client222.HasLabel(context.Background(), "acme/two", 1, "x"); err != nil {
			t.Fatal(err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	if tokenCalls["111"] != 1 || tokenCalls["222"] != 1 {
		t.Fatalf("token endpoint calls = %#v, want one per installation", tokenCalls)
	}
	for _, auth := range authorizations["/repos/acme/one/issues/1/labels"] {
		if auth != "Bearer token-111" {
			t.Fatalf("installation 111 request used %q", auth)
		}
	}
	for _, auth := range authorizations["/repos/acme/two/issues/1/labels"] {
		if auth != "Bearer token-222" {
			t.Fatalf("installation 222 request used %q", auth)
		}
	}
}

func TestManagerClientForRepoDiscoversAndCachesInstallation(t *testing.T) {
	var discoveryCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/Acme/Repo/installation" {
			http.NotFound(w, r)
			return
		}
		discoveryCalls++
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Fatal("repository discovery did not use App JWT")
		}
		_, _ = w.Write([]byte(`{"id":222}`))
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL)

	first, err := manager.ClientForRepo(context.Background(), "Acme/Repo")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.ClientForRepo(context.Background(), "acme/repo")
	if err != nil {
		t.Fatal(err)
	}
	if first.InstallationID != 222 || second.InstallationID != 222 || discoveryCalls != 1 {
		t.Fatalf("installation IDs = %d,%d discovery calls=%d", first.InstallationID, second.InstallationID, discoveryCalls)
	}
}

func TestManagerTokenFetchHonorsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL)
	client, err := manager.ClientForInstallation(context.Background(), 111)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.HasLabel(ctx, "acme/repo", 1, "x"); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("HasLabel error = %v, want context canceled", err)
	}
}

func TestManagerTaskGitCredentialIsRepositoryRestrictedAndCached(t *testing.T) {
	var calls int
	var payload struct {
		Repositories []string          `json:"repositories"`
		Permissions  map[string]string `json:"permissions"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations/111/access_tokens" {
			http.NotFound(w, r)
			return
		}
		calls++
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode credential payload: %v", err)
		}
		writeTokenResponse(w, "restricted-token")
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL)
	client, err := manager.ClientForInstallation(context.Background(), 111)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		credential, err := client.CredentialForRepo(context.Background(), "Acme/Repo", PermissionProfileTaskGit)
		if err != nil {
			t.Fatal(err)
		}
		if credential.Token != "restricted-token" || credential.ExpiresAt.IsZero() {
			t.Fatalf("credential = %+v", credential)
		}
	}
	if calls != 1 {
		t.Fatalf("credential endpoint calls = %d, want 1", calls)
	}
	if len(payload.Repositories) != 1 || payload.Repositories[0] != "Repo" {
		t.Fatalf("repositories = %#v, want [Repo]", payload.Repositories)
	}
	wantPermissions := map[string]string{"contents": "write", "issues": "read", "pull_requests": "read"}
	if fmt.Sprint(payload.Permissions) != fmt.Sprint(wantPermissions) {
		t.Fatalf("permissions = %#v, want %#v", payload.Permissions, wantPermissions)
	}
	if _, explicitMetadata := payload.Permissions["metadata"]; explicitMetadata {
		t.Fatal("metadata permission must remain implicit")
	}
}

func TestManagerTaskGitCredentialCacheIncludesRepository(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		writeTokenResponse(w, fmt.Sprintf("token-%d", calls))
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL)
	client, err := manager.ClientForInstallation(context.Background(), 111)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.CredentialForRepo(context.Background(), "Acme/One", PermissionProfileTaskGit)
	if err != nil {
		t.Fatal(err)
	}
	second, err := client.CredentialForRepo(context.Background(), "Acme/Two", PermissionProfileTaskGit)
	if err != nil {
		t.Fatal(err)
	}
	otherInstallation, err := manager.ClientForInstallation(context.Background(), 222)
	if err != nil {
		t.Fatal(err)
	}
	third, err := otherInstallation.CredentialForRepo(context.Background(), "Acme/One", PermissionProfileTaskGit)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || first.Token == second.Token || first.Token == third.Token {
		t.Fatalf("calls=%d credentials=%q/%q/%q", calls, first.Token, second.Token, third.Token)
	}
}

func TestManagerTaskGitCredentialRefreshIsPerKey(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		writeTokenResponse(w, "shared-token")
	}))
	defer server.Close()
	manager := newTestManager(t, server.URL)
	client, err := manager.ClientForInstallation(context.Background(), 111)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			credential, err := client.CredentialForRepo(context.Background(), "Acme/Repo", PermissionProfileTaskGit)
			if err == nil && credential.Token != "shared-token" {
				err = fmt.Errorf("token = %q", credential.Token)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("credential exchanges = %d, want 1", calls.Load())
	}
}
