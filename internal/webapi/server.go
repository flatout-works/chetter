package webapi

import (
	"context"
	"database/sql"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	apiv1connect "github.com/flatout-works/chetter/gen/proto/api/v1/apiv1connect"
	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/service"
)

// Handlers holds all ConnectRPC handler implementations.
type Handlers struct {
	Task    *taskHandler
	Event   *eventHandler
	Session *sessionHandler
	Trigger *triggerHandler
	Fleet   *fleetHandler
	Admin   *adminHandler
	Arcane  *arcaneHandler
	Catalog *catalogHandler
}

// NewHandlers creates all ConnectRPC handlers wrapping the shared service.
func NewHandlers(svc *service.Service, bus *EventBus) *Handlers {
	return &Handlers{
		Task:    &taskHandler{svc: svc, bus: bus},
		Event:   &eventHandler{svc: svc},
		Session: &sessionHandler{svc: svc},
		Trigger: &triggerHandler{svc: svc},
		Fleet:   &fleetHandler{svc: svc, bus: bus},
		Admin:   &adminHandler{svc: svc},
		Arcane:  &arcaneHandler{svc: svc},
		Catalog: &catalogHandler{svc: svc},
	}
}

// RegisterHandlers mounts all ConnectRPC service handlers on the given mux.
// The ArcaneService is only registered if Arcane is configured.
func RegisterHandlers(mux *http.ServeMux, h *Handlers, adminToken string, db *sql.DB) {
	interceptor := NewAuthInterceptor(adminToken, db)

	mux.Handle(apiv1connect.NewTaskServiceHandler(h.Task, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewEventServiceHandler(h.Event, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewSessionServiceHandler(h.Session, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewTriggerServiceHandler(h.Trigger, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewFleetServiceHandler(h.Fleet, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewAdminServiceHandler(h.Admin, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewCatalogServiceHandler(h.Catalog, connect.WithInterceptors(interceptor)))

	if h.Arcane.svc.ArcaneIsConfigured() {
		mux.Handle(apiv1connect.NewArcaneServiceHandler(h.Arcane, connect.WithInterceptors(interceptor)))
	}

	// Register the ListRepos endpoint with auth middleware.
	mux.HandleFunc("/api/v1/repos", authMiddleware(adminToken, db, h.Admin.HandleListRepos))
}

// Ensure the handler types satisfy the generated interfaces.
var (
	_ apiv1connect.TaskServiceHandler    = (*taskHandler)(nil)
	_ apiv1connect.EventServiceHandler   = (*eventHandler)(nil)
	_ apiv1connect.SessionServiceHandler = (*sessionHandler)(nil)
	_ apiv1connect.TriggerServiceHandler = (*triggerHandler)(nil)
	_ apiv1connect.FleetServiceHandler   = (*fleetHandler)(nil)
	_ apiv1connect.AdminServiceHandler   = (*adminHandler)(nil)
	_ apiv1connect.ArcaneServiceHandler  = (*arcaneHandler)(nil)
	_ apiv1connect.CatalogServiceHandler = (*catalogHandler)(nil)
)

// authMiddleware wraps an http.HandlerFunc with bearer token validation,
// mirroring the authInterceptor used by ConnectRPC handlers.
func authMiddleware(adminToken string, db *sql.DB, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := ""
		v := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(v) > len(prefix) && strings.HasPrefix(v, prefix) {
			token = strings.TrimPrefix(v, prefix)
		}
		scope, ok := auth.ResolveToken(r.Context(), adminToken, db, token)
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := auth.WithScope(r.Context(), scope)
		next(w, r.WithContext(ctx))
	}
}

var _ = context.Background
var _ = http.StatusNotFound
