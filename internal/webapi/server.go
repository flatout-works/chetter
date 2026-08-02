package webapi

import (
	"database/sql"
	"net/http"

	"connectrpc.com/connect"
	apiv1connect "github.com/flatout-works/chetter/gen/proto/api/v1/apiv1connect"
	"github.com/flatout-works/chetter/internal/auth"
	"github.com/flatout-works/chetter/internal/service"
)

// Handlers holds all ConnectRPC handler implementations.
type Handlers struct {
	Task          *taskHandler
	Event         *eventHandler
	Session       *sessionHandler
	Trigger       *triggerHandler
	EventCallback *eventCallbackHandler
	Fleet         *fleetHandler
	Admin         *adminHandler
	Arcane        *arcaneHandler
	Catalog       *catalogHandler
}

// NewHandlers creates all ConnectRPC handlers wrapping the shared service.
func NewHandlers(svc *service.Service, bus *EventBus) *Handlers {
	return &Handlers{
		Task:          &taskHandler{svc: svc, bus: bus},
		Event:         &eventHandler{svc: svc},
		Session:       &sessionHandler{svc: svc},
		Trigger:       &triggerHandler{svc: svc},
		EventCallback: &eventCallbackHandler{svc: svc},
		Fleet:         &fleetHandler{svc: svc, bus: bus},
		Admin:         &adminHandler{svc: svc},
		Arcane:        &arcaneHandler{svc: svc},
		Catalog:       &catalogHandler{svc: svc},
	}
}

// RegisterHandlers mounts all ConnectRPC service handlers on the given mux.
// The ArcaneService is only registered if Arcane is configured. When oidc is
// non-nil the web UI OIDC login flow is registered and session cookies are
// accepted alongside bearer tokens.
func RegisterHandlers(mux *http.ServeMux, h *Handlers, adminToken string, db *sql.DB, oidc *auth.OIDCAuth) {
	interceptor := NewAuthInterceptor(adminToken, db, oidc)

	mux.Handle(apiv1connect.NewTaskServiceHandler(h.Task, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewEventServiceHandler(h.Event, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewSessionServiceHandler(h.Session, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewTriggerServiceHandler(h.Trigger, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewEventCallbackServiceHandler(h.EventCallback, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewFleetServiceHandler(h.Fleet, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewAdminServiceHandler(h.Admin, connect.WithInterceptors(interceptor)))
	mux.Handle(apiv1connect.NewCatalogServiceHandler(h.Catalog, connect.WithInterceptors(interceptor)))

	if h.Arcane.svc.ArcaneIsConfigured() {
		mux.Handle(apiv1connect.NewArcaneServiceHandler(h.Arcane, connect.WithInterceptors(interceptor)))
	}

	// Register the ListRepos endpoint with auth middleware.
	mux.HandleFunc("/api/v1/repos", authMiddleware(adminToken, db, oidc, h.Admin.HandleListRepos))

	// Web UI OIDC login flow (no-op when OIDC is not configured).
	RegisterOIDCRoutes(mux, oidc, db)
}

// Ensure the handler types satisfy the generated interfaces.
var (
	_ apiv1connect.TaskServiceHandler          = (*taskHandler)(nil)
	_ apiv1connect.EventServiceHandler         = (*eventHandler)(nil)
	_ apiv1connect.SessionServiceHandler       = (*sessionHandler)(nil)
	_ apiv1connect.TriggerServiceHandler       = (*triggerHandler)(nil)
	_ apiv1connect.EventCallbackServiceHandler = (*eventCallbackHandler)(nil)
	_ apiv1connect.FleetServiceHandler         = (*fleetHandler)(nil)
	_ apiv1connect.AdminServiceHandler         = (*adminHandler)(nil)
	_ apiv1connect.ArcaneServiceHandler        = (*arcaneHandler)(nil)
	_ apiv1connect.CatalogServiceHandler       = (*catalogHandler)(nil)
)

// authMiddleware wraps an http.HandlerFunc with bearer token validation,
// mirroring the authInterceptor used by ConnectRPC handlers. When oidc is
// non-nil a valid session cookie is accepted as a fallback.
func authMiddleware(adminToken string, db *sql.DB, oidc *auth.OIDCAuth, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scope, ok := auth.ResolveToken(r.Context(), adminToken, db, bearerToken(r.Header))
		if !ok && oidc != nil {
			scope, ok = oidc.ScopeFromCookie(r.Header)
		}
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := auth.WithScope(r.Context(), scope)
		next(w, r.WithContext(ctx))
	}
}
