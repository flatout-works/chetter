package webhook

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type githubTestTransport struct {
	base *url.URL
}

func (t githubTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = t.base.Scheme
	cloned.URL.Host = t.base.Host
	return http.DefaultTransport.RoundTrip(cloned)
}

func TestGetAppLoginUsesAppJWTAndCachesSuccess(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	var calls int
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"slug":"chetterbot"}`))
	}))
	defer server.Close()
	base, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}

	client := &Client{
		AppID:      123,
		PrivateKey: key,
		HTTPClient: &http.Client{Transport: githubTestTransport{base: base}},
	}
	login, err := client.GetAppLogin(context.Background())
	if err != nil {
		t.Fatalf("GetAppLogin returned error: %v", err)
	}
	if login != "chetterbot[bot]" {
		t.Fatalf("GetAppLogin = %q, want chetterbot[bot]", login)
	}
	if !strings.HasPrefix(authorization, "Bearer ") || strings.Count(strings.TrimPrefix(authorization, "Bearer "), ".") != 2 {
		t.Fatalf("expected JWT authorization, got %q", authorization)
	}

	login, err = client.GetAppLogin(context.Background())
	if err != nil {
		t.Fatalf("cached GetAppLogin returned error: %v", err)
	}
	if login != "chetterbot[bot]" || calls != 1 {
		t.Fatalf("cached GetAppLogin = %q after %d requests, want one request", login, calls)
	}
}
