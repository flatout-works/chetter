package webapi

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	apiv1 "github.com/flatout-works/chetter/gen/proto/api/v1"
)

const (
	taskEventPollInterval = time.Second
	fleetPollInterval     = 3 * time.Second
	// taskEventDedupCap bounds the sent-ID dedup map on very long streams.
	// Beyond the cap the map resets; a rare duplicate delivery afterward is
	// harmless because clients treat the event log as append-only.
	taskEventDedupCap = 4096
)

// rememberSent records an event ID for duplicate suppression, resetting the
// map once it exceeds taskEventDedupCap so memory stays bounded on very long
// streams.
func rememberSent(sent map[string]struct{}, id string) {
	if id == "" {
		return
	}
	if len(sent) >= taskEventDedupCap {
		for k := range sent {
			delete(sent, k)
		}
	}
	sent[id] = struct{}{}
}

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

	dbCursor := since
	if dbCursor.IsZero() {
		// An empty cursor means "new events from this subscription", not full
		// history. Capture the boundary before subscribing to avoid a gap.
		dbCursor = time.Now().UTC()
	}
	sent := make(map[string]struct{})

	// Subscribe before replaying history so local events committed during the
	// replay are buffered. The DB poller below also catches events written by
	// other server replicas.
	var ch <-chan *apiv1.TaskEvent
	unsub := func() {}
	if h.bus != nil {
		ch, unsub = h.bus.SubscribeTaskEvents(taskID, 64)
	}
	defer unsub()

	// Phase 1: Replay historical events from DB.
	if !since.IsZero() {
		events, err := h.svc.GetTaskEventsSince(ctx, taskID, since)
		if err != nil {
			return connect.NewError(connect.CodeInternal, err)
		}
		for _, e := range events {
			if e.CreatedAt.After(dbCursor) {
				dbCursor = e.CreatedAt
			}
			rememberSent(sent, e.ID)
			if err := stream.Send(protoEvent(e)); err != nil {
				return err
			}
		}
	}

	// Phase 2: merge low-latency local notifications with durable DB polling.
	pollTicker := time.NewTicker(taskEventPollInterval)
	defer pollTicker.Stop()
	keepaliveTicker := time.NewTicker(15 * time.Second)
	defer keepaliveTicker.Stop()
	var lastPollWarn time.Time

	for {
		select {
		case event := <-ch:
			if event == nil {
				continue
			}
			if _, duplicate := sent[event.Id]; duplicate && event.Id != "" {
				continue
			}
			if event.Id != "" {
				rememberSent(sent, event.Id)
			}
			if err := stream.Send(event); err != nil {
				return err
			}
		case <-pollTicker.C:
			// Overlap one database timestamp quantum so events sharing the
			// cursor's microsecond are not missed; sent IDs remove duplicates.
			events, err := h.svc.GetTaskEventsSince(ctx, taskID, dbCursor.Add(-time.Microsecond))
			if err != nil {
				// Degrade rather than fail: the local bus path may still be
				// delivering events, and killing the stream on a transient DB
				// error would disconnect every viewer during a failover. The
				// next tick retries; the client can always reconnect with a
				// cursor to recover missed events.
				if time.Since(lastPollWarn) > time.Minute {
					lastPollWarn = time.Now()
					slog.Warn("task event poll failed; retrying", "task_id", taskID, "err", err)
				}
				continue
			}
			for _, e := range events {
				if e.CreatedAt.After(dbCursor) {
					dbCursor = e.CreatedAt
				}
				if _, duplicate := sent[e.ID]; duplicate {
					continue
				}
				rememberSent(sent, e.ID)
				if err := stream.Send(protoEvent(e)); err != nil {
					return err
				}
			}
		case <-keepaliveTicker.C:
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
// runner registrations/losses) to the client. Updates come from the local
// event bus, fed both by same-replica task events and by the per-replica
// fleet-cursor poller that detects task activity committed by other server
// replicas (see fleet_poller.go).
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

	keepaliveTicker := time.NewTicker(15 * time.Second)
	defer keepaliveTicker.Stop()

	for {
		select {
		case update := <-ch:
			if update == nil {
				continue
			}
			if err := stream.Send(update); err != nil {
				return err
			}
		case <-keepaliveTicker.C:
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
