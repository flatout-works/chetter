package inbound

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory inbound.Store for receiver tests.
type fakeStore struct {
	mu           sync.Mutex
	endpoints    map[string]Endpoint // keyed by public id
	pendingCount int
	inserts      []DeliveryInsert
	err          error
}

func newFakeStore() *fakeStore {
	return &fakeStore{endpoints: map[string]Endpoint{}}
}

func (f *fakeStore) GetEndpointByPublicID(_ context.Context, publicID string) (Endpoint, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return Endpoint{}, false, f.err
	}
	endpoint, ok := f.endpoints[publicID]
	return endpoint, ok, nil
}

func (f *fakeStore) InsertDelivery(_ context.Context, insert DeliveryInsert) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return false, f.err
	}
	for _, existing := range f.inserts {
		if existing.EndpointID == insert.EndpointID && insert.DeliveryID != "" && existing.DeliveryID == insert.DeliveryID {
			return false, nil
		}
	}
	f.inserts = append(f.inserts, insert)
	return true, nil
}

func (f *fakeStore) PendingDeliveryCount(_ context.Context, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	return f.pendingCount, nil
}

func hmacEndpoint(publicID, secret string) Endpoint {
	return Endpoint{
		ID:               "inb_1",
		PublicID:         publicID,
		Name:             "ci-events",
		TeamID:           "team_1",
		Enabled:          true,
		AuthType:         "hmac_sha256",
		SecretEnv:        "CHETTER_TEST_SECRET",
		SignatureHeader:  "X-CI-Signature",
		SignaturePrefix:  "sha256=",
		DeliveryIDHeader: "X-Delivery-ID",
		EventTypeHeader:  "X-Event-Type",
	}
}

func bearerEndpoint(publicID string) Endpoint {
	return Endpoint{
		ID:        "inb_2",
		PublicID:  publicID,
		Name:      "bearer-endpoint",
		Enabled:   true,
		AuthType:  "bearer",
		SecretEnv: "CHETTER_TEST_TOKEN",
	}
}

func signature(t *testing.T, secret, body string) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestHandlerAcceptsHMACRequest(t *testing.T) {
	store := newFakeStore()
	endpoint := hmacEndpoint("pub_1", "")
	store.endpoints["pub_1"] = endpoint
	handler := NewHandler(Config{
		SecretEnv: func(name string) (string, bool) {
			if name == "CHETTER_TEST_SECRET" {
				return "s3cr3t", true
			}
			return "", false
		},
	}, store, nil)

	body := `{"repository":"acme/web","build_url":"https://ci.example.com/build/1"}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/inbound/pub_1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CI-Signature", signature(t, "s3cr3t", body))
	req.Header.Set("X-Delivery-ID", "dvr-123")
	req.Header.Set("X-Event-Type", "build.completed")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body %s", rec.Code, rec.Body.String())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.inserts) != 1 {
		t.Fatalf("inserts = %d, want 1", len(store.inserts))
	}
	insert := store.inserts[0]
	if insert.EndpointID != "inb_1" || insert.DeliveryID != "dvr-123" || insert.EventType != "build.completed" {
		t.Errorf("insert = %+v", insert)
	}
	if string(insert.Payload) != body {
		t.Errorf("payload mismatch")
	}
}

func TestHandlerBearerAuth(t *testing.T) {
	store := newFakeStore()
	store.endpoints["pub_2"] = bearerEndpoint("pub_2")
	handler := NewHandler(Config{
		SecretEnv: func(name string) (string, bool) {
			if name == "CHETTER_TEST_TOKEN" {
				return "tok-123", true
			}
			return "", false
		},
	}, store, nil)

	req := httptest.NewRequest(http.MethodPost, "/hooks/inbound/pub_2", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer tok-123")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}

	// Wrong token -> 401 and no insertion.
	store.mu.Lock()
	before := len(store.inserts)
	store.mu.Unlock()
	req = httptest.NewRequest(http.MethodPost, "/hooks/inbound/pub_2", strings.NewReader(`{"a":1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer nope")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad token status = %d, want 401", rec.Code)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.inserts) != before {
		t.Fatal("bad token must not insert a delivery")
	}
}

func TestHandlerRejects(t *testing.T) {
	store := newFakeStore()
	endpoint := hmacEndpoint("pub_1", "")
	store.endpoints["pub_1"] = endpoint
	handler := NewHandler(Config{
		SecretEnv: func(name string) (string, bool) {
			return "s3cr3t", true
		},
	}, store, nil)

	body := `{"a":1}`
	sig := signature(t, "s3cr3t", body)

	post := func(path string, mutate func(*http.Request)) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CI-Signature", sig)
		if mutate != nil {
			mutate(req)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	tests := []struct {
		name string
		rec  *httptest.ResponseRecorder
		want int
	}{
		{"wrong method", func() *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodGet, "/hooks/inbound/pub_1", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			return rec
		}(), http.StatusMethodNotAllowed},
		{"unknown public id", post("/hooks/inbound/missing", nil), http.StatusNotFound},
		{"bad signature", post("/hooks/inbound/pub_1", func(r *http.Request) {
			r.Header.Set("X-CI-Signature", "sha256=deadbeef")
		}), http.StatusUnauthorized},
		{"wrong content type", post("/hooks/inbound/pub_1", func(r *http.Request) {
			r.Header.Set("Content-Type", "text/plain")
		}), http.StatusUnsupportedMediaType},
		{"missing content type", post("/hooks/inbound/pub_1", func(r *http.Request) {
			r.Header.Del("Content-Type")
		}), http.StatusUnsupportedMediaType},
		{"empty body", post("/hooks/inbound/pub_1", func(r *http.Request) {
			r.Body = http.NoBody
		}), http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", tt.rec.Code, tt.want)
			}
		})
	}
}

func TestHandlerDisabledEndpointIsNotFound(t *testing.T) {
	store := newFakeStore()
	endpoint := hmacEndpoint("pub_1", "")
	endpoint.Enabled = false
	store.endpoints["pub_1"] = endpoint
	handler := NewHandler(Config{
		SecretEnv: func(name string) (string, bool) {
			return "s3cr3t", true
		},
	}, store, nil)
	req := httptest.NewRequest(http.MethodPost, "/hooks/inbound/pub_1", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled endpoint status = %d, want 404", rec.Code)
	}
}

func TestHandlerSecretNotConfiguredIs503(t *testing.T) {
	store := newFakeStore()
	store.endpoints["pub_1"] = hmacEndpoint("pub_1", "")
	handler := NewHandler(Config{
		SecretEnv: func(name string) (string, bool) {
			return "", false
		},
	}, store, nil)
	req := httptest.NewRequest(http.MethodPost, "/hooks/inbound/pub_1", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured secret status = %d, want 503", rec.Code)
	}
}

func TestHandlerReplayIsIdempotent(t *testing.T) {
	store := newFakeStore()
	store.endpoints["pub_1"] = hmacEndpoint("pub_1", "")
	handler := NewHandler(Config{
		SecretEnv: func(name string) (string, bool) {
			return "s3cr3t", true
		},
	}, store, nil)

	body := `{"a":1}`
	sig := signature(t, "s3cr3t", body)
	send := func() int {
		req := httptest.NewRequest(http.MethodPost, "/hooks/inbound/pub_1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CI-Signature", sig)
		req.Header.Set("X-Delivery-ID", "dvr-replay")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := send(); code != http.StatusAccepted {
		t.Fatalf("first send status = %d, want 202", code)
	}
	if code := send(); code != http.StatusAccepted {
		t.Fatalf("replay status = %d, want 202 (idempotent)", code)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.inserts) != 1 {
		t.Fatalf("replay inserted %d rows, want 1", len(store.inserts))
	}
}

func TestHandlerBodySizeLimit(t *testing.T) {
	store := newFakeStore()
	store.endpoints["pub_1"] = hmacEndpoint("pub_1", "")
	handler := NewHandler(Config{
		MaxBodyBytes: 16,
		SecretEnv: func(name string) (string, bool) {
			return "s3cr3t", true
		},
	}, store, nil)
	big := `{"payload":"` + strings.Repeat("x", 64) + `"}`
	sig := signature(t, "s3cr3t", big)
	req := httptest.NewRequest(http.MethodPost, "/hooks/inbound/pub_1", strings.NewReader(big))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CI-Signature", sig)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body status = %d, want 413", rec.Code)
	}
}

func TestHandlerRateLimit(t *testing.T) {
	store := newFakeStore()
	store.endpoints["pub_1"] = hmacEndpoint("pub_1", "")
	handler := NewHandler(Config{
		RateLimit:  2,
		RateWindow: time.Minute,
		SecretEnv: func(name string) (string, bool) {
			return "s3cr3t", true
		},
	}, store, nil)
	body := `{"a":1}`
	sig := signature(t, "s3cr3t", body)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/hooks/inbound/pub_1", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-CI-Signature", sig)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("request %d status = %d, want 202", i+1, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/hooks/inbound/pub_1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CI-Signature", sig)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate limited request status = %d, want 429", rec.Code)
	}
}

func TestHandlerBacklogLimit(t *testing.T) {
	store := newFakeStore()
	store.endpoints["pub_1"] = hmacEndpoint("pub_1", "")
	store.pendingCount = 200
	handler := NewHandler(Config{
		MaxPending: 200,
		SecretEnv: func(name string) (string, bool) {
			return "s3cr3t", true
		},
	}, store, nil)
	body := `{"a":1}`
	sig := signature(t, "s3cr3t", body)
	req := httptest.NewRequest(http.MethodPost, "/hooks/inbound/pub_1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CI-Signature", sig)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("backlogged request status = %d, want 429", rec.Code)
	}
}

func TestHandlerAuditsAuthFailureAndReceipt(t *testing.T) {
	store := newFakeStore()
	store.endpoints["pub_1"] = hmacEndpoint("pub_1", "")
	var events []string
	var mu sync.Mutex
	handler := NewHandler(Config{
		SecretEnv: func(name string) (string, bool) {
			return "s3cr3t", true
		},
	}, store, func(_ context.Context, eventType, _, _, _ string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, eventType)
	})

	body := `{"a":1}`
	req := httptest.NewRequest(http.MethodPost, "/hooks/inbound/pub_1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CI-Signature", "sha256=bad")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	req = httptest.NewRequest(http.MethodPost, "/hooks/inbound/pub_1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CI-Signature", signature(t, "s3cr3t", body))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	mu.Lock()
	defer mu.Unlock()
	want := []string{"inbound_auth_failed", "inbound_delivery_received"}
	if len(events) != len(want) {
		t.Fatalf("audit events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("audit events = %v, want %v", events, want)
		}
	}
}
