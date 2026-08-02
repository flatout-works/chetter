// Package oidctest provides a fake OpenID Connect provider for tests. It
// serves provider discovery, JWKS, and a token endpoint, and signs ID tokens
// with an RSA key whose public half is published in the JWKS — enough for
// go-oidc's verifier to run the real signature/issuer/audience checks.
package oidctest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

// TokenSpec describes an ID token the fake provider will mint for a code.
type TokenSpec struct {
	Subject       string
	Email         string
	EmailVerified *bool
	Groups        []string
	Nonce         string
}

// FakeProvider is a minimal OIDC provider for tests.
type FakeProvider struct {
	Server *httptest.Server
	Issuer string

	// NoEndSession omits end_session_endpoint from the discovery document
	// when set before the provider is constructed.
	NoEndSession bool

	mu     sync.Mutex
	codes  map[string]TokenSpec
	key    *rsa.PrivateKey
	kid    string
	client string
}

// New starts a fake provider. The provider is shut down with t.Cleanup.
func New(t *testing.T, clientID string) *FakeProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	f := &FakeProvider{
		codes:  make(map[string]TokenSpec),
		key:    key,
		kid:    "test-key",
		client: clientID,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", f.handleDiscovery)
	mux.HandleFunc("/jwks", f.handleJWKS)
	mux.HandleFunc("/token", f.handleToken)
	mux.HandleFunc("/authorize", f.handleAuthorize)
	f.Server = httptest.NewServer(mux)
	f.Issuer = f.Server.URL
	t.Cleanup(f.Server.Close)
	return f
}

// IssueCode registers a token spec and returns an authorization code that the
// token endpoint will redeem for the corresponding ID token.
func (f *FakeProvider) IssueCode(spec TokenSpec) string {
	code := randomHex(16)
	f.mu.Lock()
	f.codes[code] = spec
	f.mu.Unlock()
	return code
}

// SignIDToken mints a signed ID token for the given spec without needing a
// code, useful for negative tests.
func (f *FakeProvider) SignIDToken(spec TokenSpec) string {
	token, err := f.signIDToken(spec)
	if err != nil {
		panic(err)
	}
	return token
}

func (f *FakeProvider) handleDiscovery(w http.ResponseWriter, r *http.Request) {
	doc := map[string]any{
		"issuer":                 f.Issuer,
		"authorization_endpoint": f.Issuer + "/authorize",
		"token_endpoint":         f.Issuer + "/token",
		"jwks_uri":               f.Issuer + "/jwks",
		"response_types_supported": []string{"code"},
		"subject_types_supported":  []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	}
	if !f.NoEndSession {
		doc["end_session_endpoint"] = f.Issuer + "/logout"
	}
	_ = json.NewEncoder(w).Encode(doc)
}

func (f *FakeProvider) handleJWKS(w http.ResponseWriter, r *http.Request) {
	n := base64.RawURLEncoding.EncodeToString(f.key.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})
	_ = json.NewEncoder(w).Encode(map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": f.kid,
			"n":   n,
			"e":   e,
		}},
	})
}

func (f *FakeProvider) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	// Tests never navigate here; the login redirect only builds the URL.
	http.Error(w, "authorize endpoint not implemented", http.StatusNotImplemented)
}

func (f *FakeProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	code := r.Form.Get("code")
	f.mu.Lock()
	spec, ok := f.codes[code]
	delete(f.codes, code)
	f.mu.Unlock()
	if !ok {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		return
	}
	idToken, err := f.signIDToken(spec)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": randomHex(24),
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     idToken,
	})
}

func (f *FakeProvider) signIDToken(spec TokenSpec) (string, error) {
	now := time.Now().Unix()
	claims := map[string]any{
		"iss":   f.Issuer,
		"sub":   spec.Subject,
		"aud":   f.client,
		"iat":   now,
		"exp":   now + 3600,
		"email": spec.Email,
	}
	if spec.EmailVerified != nil {
		claims["email_verified"] = *spec.EmailVerified
	}
	if len(spec.Groups) > 0 {
		claims["groups"] = spec.Groups
	}
	if spec.Nonce != "" {
		claims["nonce"] = spec.Nonce
	}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": f.kid, "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, hash[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

// Query extracts a query parameter for assertions.
func Query(rawURL, key string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Query().Get(key)
}
