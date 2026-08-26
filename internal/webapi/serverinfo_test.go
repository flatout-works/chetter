package webapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestServerInfoRedactsDetailsWithoutAuthentication(t *testing.T) {
	handler := NewServerInfoHandler(testServerInfoConfig())
	req := httptest.NewRequest(http.MethodGet, "/api/server-info", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["allowTokenLogin"] != false || body["oidcEnabled"] != false {
		t.Errorf("public auth capabilities = %v", body)
	}
	for _, key := range []string{"serverVersion", "gitHash", "uptimeSeconds", "startedAt", "quotaExhausted", "lastReapAt", "dbSessionTimeZone"} {
		if _, ok := body[key]; ok {
			t.Errorf("unauthenticated response exposed %q: %v", key, body[key])
		}
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

func TestServerInfoReturnsDetailsWithAdminToken(t *testing.T) {
	handler := NewServerInfoHandler(testServerInfoConfig())
	req := httptest.NewRequest(http.MethodGet, "/api/server-info", nil)
	req.Header.Set("Authorization", "Bearer admin-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["serverVersion"] != "v1.2.3" || body["gitHash"] != "abc123" {
		t.Errorf("authenticated response = %v", body)
	}
	if body["dbSessionTimeZone"] != "+00:00" || body["dbTimeZoneUTC"] != true {
		t.Errorf("database posture = %v", body)
	}
}

func TestServerInfoRejectsOtherMethods(t *testing.T) {
	handler := NewServerInfoHandler(testServerInfoConfig())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/server-info", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

func testServerInfoConfig() ServerInfoConfig {
	started := time.Date(2026, time.August, 26, 10, 0, 0, 0, time.UTC)
	reaped := started.Add(time.Minute)
	return ServerInfoConfig{
		AdminToken:      "admin-token",
		Version:         func() string { return "v1.2.3" },
		GitHash:         func() string { return "abc123" },
		UptimeSeconds:   func() int64 { return 42 },
		StartedAt:       func() time.Time { return started },
		QuotaExhausted:  func() bool { return true },
		LastReapAt:      func() time.Time { return reaped },
		AllowTokenLogin: false,
		DBSessionTZ:     "+00:00",
		DBGlobalTZ:      "UTC",
		DBTimeZoneUTC:   true,
	}
}
