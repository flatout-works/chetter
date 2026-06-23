package webapi

import (
	"context"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/flatout-works/chetter/gen/proto/api/v1"
)

// SubscribeTaskEvents streams task events to the client. It first replays
// any historical events since the `since` cursor, then switches to the
// live event bus for real-time delivery. A keepalive ping is sent every
// 15 seconds when idle to prevent connection timeouts.
func (h *taskHandler) SubscribeTaskEvents(
	ctx context.Context,
	req *connect.Request[apiv1.SubscribeTaskEventsRequest],
	stream *connect.ServerStream[apiv1.TaskEvent],
) error {
	taskID := req.Msg.TaskId
	since := parseTime(req.Msg.Since)
	if _, err := h.svc.GetTask(ctx, taskID); err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}

	// Phase 1: Replay historical events from DB.
	if !since.IsZero() {
		events, err := h.svc.GetTaskEventsSince(ctx, taskID, since)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err)
		}
		for _, e := range events {
			if err := stream.Send(protoEvent(e)); err != nil {
				return err
			}
		}
	}

	// Phase 2: Subscribe to live events.
	if h.bus == nil {
		<-ctx.Done()
		return ctx.Err()
	}

	ch, unsub := h.bus.SubscribeTaskEvents(taskID, 64)
	defer unsub()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event := <-ch:
			if err := stream.Send(event); err != nil {
				return err
			}
		case <-ticker.C:
			// Keepalive: send an empty event so the client knows we're alive.
			if err := stream.Send(&apiv1.TaskEvent{
				TaskId:    taskID,
				Status:    "keepalive",
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// SubscribeFleetUpdates streams fleet-wide updates (task status changes,
// runner registrations/losses) to the client.
func (h *fleetHandler) SubscribeFleetUpdates(
	ctx context.Context,
	req *connect.Request[apiv1.SubscribeFleetUpdatesRequest],
	stream *connect.ServerStream[apiv1.FleetUpdate],
) error {
	if h.bus == nil {
		<-ctx.Done()
		return ctx.Err()
	}

	ch, unsub := h.bus.SubscribeFleetUpdates(64)
	defer unsub()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case update := <-ch:
			if err := stream.Send(update); err != nil {
				return err
			}
		case <-ticker.C:
			if err := stream.Send(&apiv1.FleetUpdate{
				Type: "keepalive",
			}); err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

