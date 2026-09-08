package service

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/flatout-works/chetter/internal/inbound"
	"github.com/flatout-works/chetter/internal/store"
)

// NewInboundWebhookReceiver builds the HTTP receiver for generic inbound
// webhook endpoints (issue #120). It resolves the endpoint by its opaque
// public id, authenticates the request with the secret referenced by
// auth.secret_env (never stored), enforces size/rate/backlog limits, durably
// inserts an inbound delivery row, and only then answers 202. Returns nil when
// the database is unavailable so callers can skip route registration.
func NewInboundWebhookReceiver(db *sql.DB, dialect store.Dialect, svc *Service) http.Handler {
	if db == nil || svc == nil {
		return nil
	}
	storeAdapter := newInboundStore(db, dialect)
	var audit inbound.AuditFunc
	if svc != nil {
		audit = func(ctx context.Context, eventType, sourceID, targetID, detail string) {
			svc.auditAsync(ctx, AuditEventParams{
				EventType:  eventType,
				SourceType: "inbound",
				SourceID:   sourceID,
				TargetType: "webhook_endpoint",
				TargetID:   targetID,
				Detail:     detail,
			})
		}
	}
	return inbound.NewHandler(inbound.Config{}, storeAdapter, audit)
}
