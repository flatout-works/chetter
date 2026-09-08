// Package inbound implements the generic inbound webhook receiver (issue
// #120, epic #253 Phase 2). External systems POST authenticated JSON events
// to /hooks/inbound/<public_id>; the receiver validates and durably enqueues
// the request into the inbound_deliveries inbox before answering 202, and a
// leased worker elsewhere performs the endpoint action (create_task) exactly
// once.
//
// The receiver never stores or logs secret material: endpoint rows reference
// secrets by environment variable name only, and signature/bearer values are
// resolved from the server process environment at request time.
package inbound

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/flatout-works/chetter/internal/requestid"
)

// Endpoint is the read model of one materialized inbound endpoint that the
// receiver needs to authenticate and enqueue a request.
type Endpoint struct {
	ID               string
	PublicID         string
	Name             string
	Scope            string
	TeamID           string
	Enabled          bool
	AuthType         string // "hmac_sha256" | "bearer"
	SecretEnv        string
	SignatureHeader  string
	SignaturePrefix  string
	DeliveryIDHeader string
	EventTypeHeader  string
}

// DeliveryInsert is the durable inbox row the receiver writes before it
// answers 202. DeliveryID is the optional client replay key.
type DeliveryInsert struct {
	EndpointID string
	TeamID     string
	DeliveryID string
	EventType  string
	SourceIP   string
	Payload    []byte
}

// Store is the persistence boundary the receiver needs. Implementations must
// insert the delivery row durably and report duplicates of the same
// (endpoint, delivery_id) pair as created=false (replay).
type Store interface {
	// GetEndpointByPublicID resolves an endpoint by its opaque public URL
	// slug. Returns ok=false when no endpoint has that public id.
	GetEndpointByPublicID(ctx context.Context, publicID string) (Endpoint, bool, error)
	// InsertDelivery durably records one inbound request. created=false (with
	// nil error) means a delivery with the same delivery_id was already
	// recorded for this endpoint (idempotent replay).
	InsertDelivery(ctx context.Context, insert DeliveryInsert) (created bool, err error)
	// PendingDeliveryCount returns the number of not-yet-terminal deliveries
	// for an endpoint, used to apply per-endpoint backlog bounds.
	PendingDeliveryCount(ctx context.Context, endpointID string) (int, error)
}

// AuditFunc records a server-side audit event. Detail must never carry the
// payload or any secret material.
type AuditFunc func(ctx context.Context, eventType, sourceID, targetID, detail string)

// Config holds receiver limits. Zero values fall back to the defaults below.
type Config struct {
	// MaxBodyBytes bounds the accepted request body (default 1 MiB).
	MaxBodyBytes int64
	// RateLimit is the maximum number of accepted requests per endpoint per
	// RateWindow (fixed window, in-memory; default 60/minute).
	RateLimit int
	// RateWindow is the fixed-window length for RateLimit (default 1 minute).
	RateWindow time.Duration
	// MaxPending is the maximum number of queued (not yet terminal)
	// deliveries per endpoint before the receiver answers 429 (default 200).
	MaxPending int
	// SecretEnv resolves the server environment variable named by an
	// endpoint's auth.secret_env. Defaults to os.LookupEnv.
	SecretEnv func(name string) (string, bool)
}

const (
	// MaxDeliveryIDLength bounds the client-supplied replay key.
	MaxDeliveryIDLength = 128
	// MaxEventTypeLength bounds the classified event type.
	MaxEventTypeLength = 128
)

func (c Config) withDefaults() Config {
	if c.MaxBodyBytes <= 0 {
		c.MaxBodyBytes = 1 << 20 // 1 MiB
	}
	if c.RateLimit <= 0 {
		c.RateLimit = 60
	}
	if c.RateWindow <= 0 {
		c.RateWindow = time.Minute
	}
	if c.MaxPending <= 0 {
		c.MaxPending = 200
	}
	if c.SecretEnv == nil {
		c.SecretEnv = os.LookupEnv
	}
	return c
}

// Handler serves POST /hooks/inbound/<public_id>.
type Handler struct {
	cfg   Config
	store Store
	audit AuditFunc

	// limiter is a per-public-id fixed-window counter for the configured rate.
	limiterMu sync.Mutex
	limiter   map[string]*fixedWindow
}

type fixedWindow struct {
	start time.Time
	count int
}

// NewHandler builds the inbound receiver. store is required; audit may be nil
// (auditing disabled).
func NewHandler(cfg Config, store Store, audit AuditFunc) *Handler {
	cfg = cfg.withDefaults()
	return &Handler{
		cfg:     cfg,
		store:   store,
		audit:   audit,
		limiter: make(map[string]*fixedWindow),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID := requestid.FromContext(r.Context())
	if h.store == nil {
		h.reject(w, http.StatusServiceUnavailable, "receiver unavailable")
		return
	}
	publicID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/hooks/inbound/"), "/")
	if publicID == "" || strings.Contains(publicID, "/") || len(publicID) > 128 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Content-type enforcement: external events are JSON.
	contentType := r.Header.Get("Content-Type")
	if contentType == "" {
		http.Error(w, "content-type must be application/json", http.StatusUnsupportedMediaType)
		return
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if mediaType != "application/json" && mediaType != "application/*+json" && !strings.HasSuffix(mediaType, "+json") {
		http.Error(w, "content-type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	// Resolve the endpoint before doing heavier work so unknown slugs are
	// indistinguishable from disabled endpoints (no existence oracle).
	resolveCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	endpoint, ok, err := h.store.GetEndpointByPublicID(resolveCtx, publicID)
	cancel()
	if err != nil {
		slog.Warn("inbound webhook: resolve endpoint", "public_id", publicID, "err", err, "request_id", reqID)
		h.reject(w, http.StatusServiceUnavailable, "receiver unavailable")
		return
	}
	if !ok || !endpoint.Enabled {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Per-endpoint rate limit (fixed window). Applies before body read and
	// authentication so floods cannot burn CPU or DB writes.
	if !h.allow(publicID, r) {
		h.auditf(r.Context(), "inbound_request_rate_limited", endpoint.ID, endpoint.Name, "request rate limited")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, h.cfg.MaxBodyBytes+1))
	if err != nil {
		slog.Warn("inbound webhook: read body", "err", err, "public_id", publicID, "request_id", reqID)
		h.reject(w, http.StatusBadRequest, "read error")
		return
	}
	if int64(len(body)) > h.cfg.MaxBodyBytes {
		h.auditf(r.Context(), "inbound_request_rejected", endpoint.ID, endpoint.Name, "payload exceeds size limit")
		http.Error(w, "payload too large", http.StatusRequestEntityTooLarge)
		return
	}
	if len(body) == 0 {
		h.auditf(r.Context(), "inbound_request_rejected", endpoint.ID, endpoint.Name, "empty body")
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}

	// Authentication using the secret referenced by auth.secret_env. Secret
	// values never leave the process: only the pass/fail outcome is audited.
	secret, configured := h.cfg.SecretEnv(endpoint.SecretEnv)
	if !configured || secret == "" {
		slog.Warn("inbound webhook: secret env not configured", "endpoint", endpoint.Name, "secret_env", endpoint.SecretEnv, "request_id", reqID)
		h.auditf(r.Context(), "inbound_auth_unavailable", endpoint.ID, endpoint.Name, "secret environment variable not configured")
		h.reject(w, http.StatusServiceUnavailable, "receiver not configured")
		return
	}
	if !h.authenticated(endpoint, secret, r, body) {
		h.auditf(r.Context(), "inbound_auth_failed", endpoint.ID, endpoint.Name, "authentication failed")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// Per-endpoint backlog bound: refuse new work while too many accepted
	// deliveries are still queued (concurrency/backpressure control).
	pendingCtx, pendingCancel := context.WithTimeout(r.Context(), 5*time.Second)
	pending, err := h.store.PendingDeliveryCount(pendingCtx, endpoint.ID)
	pendingCancel()
	if err != nil {
		slog.Warn("inbound webhook: pending count", "err", err, "endpoint", endpoint.Name, "request_id", reqID)
		h.reject(w, http.StatusServiceUnavailable, "receiver unavailable")
		return
	}
	if pending >= h.cfg.MaxPending {
		h.auditf(r.Context(), "inbound_request_rejected", endpoint.ID, endpoint.Name, "delivery backlog limit reached")
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}

	deliveryID := headerValue(r, endpoint.DeliveryIDHeader)
	if len(deliveryID) > MaxDeliveryIDLength {
		deliveryID = deliveryID[:MaxDeliveryIDLength]
	}
	eventType := headerValue(r, endpoint.EventTypeHeader)
	if len(eventType) > MaxEventTypeLength {
		eventType = eventType[:MaxEventTypeLength]
	}
	sourceIP := clientIP(r)

	// Durable inbox write: 202 is only returned after the row committed.
	insertCtx, insertCancel := context.WithTimeout(r.Context(), 5*time.Second)
	created, err := h.store.InsertDelivery(insertCtx, DeliveryInsert{
		EndpointID: endpoint.ID,
		TeamID:     endpoint.TeamID,
		DeliveryID: deliveryID,
		EventType:  eventType,
		SourceIP:   sourceIP,
		Payload:    body,
	})
	insertCancel()
	if err != nil {
		slog.Error("inbound webhook: insert delivery", "err", err, "endpoint", endpoint.Name, "request_id", reqID)
		h.reject(w, http.StatusServiceUnavailable, "delivery queue unavailable")
		return
	}
	if !created {
		// Same delivery_id already durably accepted; answer 202 idempotently
		// without enqueueing a second action.
		h.auditf(r.Context(), "inbound_delivery_replay", endpoint.ID, endpoint.Name, "duplicate delivery_id ignored")
		w.WriteHeader(http.StatusAccepted)
		return
	}
	h.auditf(r.Context(), "inbound_delivery_received", endpoint.ID, endpoint.Name,
		fmt.Sprintf("event=%s delivery_id=%s source_ip=%s", eventType, deliveryID, sourceIP))
	w.WriteHeader(http.StatusAccepted)
}

func (h *Handler) authenticated(endpoint Endpoint, secret string, r *http.Request, body []byte) bool {
	switch endpoint.AuthType {
	case "hmac_sha256":
		received := r.Header.Get(endpoint.SignatureHeader)
		expected := hmacSHA256Hex(secret, body)
		if endpoint.SignaturePrefix != "" {
			expected = endpoint.SignaturePrefix + expected
		}
		return subtle.ConstantTimeCompare([]byte(received), []byte(expected)) == 1
	case "bearer":
		authz := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(authz, prefix) {
			return false
		}
		token := strings.TrimSpace(authz[len(prefix):])
		return subtle.ConstantTimeCompare([]byte(token), []byte(secret)) == 1
	default:
		return false
	}
}

func hmacSHA256Hex(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func headerValue(r *http.Request, name string) string {
	if name == "" {
		return ""
	}
	return strings.TrimSpace(r.Header.Get(name))
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// allow implements the per-public-id fixed-window rate limit. Slow path per
// request but negligible at configured rates.
func (h *Handler) allow(publicID string, r *http.Request) bool {
	h.limiterMu.Lock()
	defer h.limiterMu.Unlock()
	now := time.Now()
	entry, ok := h.limiter[publicID]
	if !ok || now.Sub(entry.start) >= h.cfg.RateWindow {
		// Opportunistically drop expired windows to bound memory.
		if len(h.limiter) > 4096 {
			h.limiter = make(map[string]*fixedWindow)
		}
		entry = &fixedWindow{start: now}
		h.limiter[publicID] = entry
	}
	if entry.count >= h.cfg.RateLimit {
		return false
	}
	entry.count++
	return true
}

func (h *Handler) auditf(ctx context.Context, eventType, sourceID, targetID, detail string) {
	if h.audit != nil {
		h.audit(ctx, eventType, sourceID, targetID, detail)
	}
}

func (h *Handler) reject(w http.ResponseWriter, code int, message string) {
	http.Error(w, message, code)
}
