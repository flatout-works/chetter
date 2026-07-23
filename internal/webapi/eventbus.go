package webapi

import (
	"sync"

	apiv1 "github.com/flatout-works/chetter/gen/proto/api/v1"
)

// EventBus provides in-memory fan-out of task events and fleet updates
// to subscribed streaming RPC clients. It is a best-effort notification
// layer — subscribers should use DB queries as the source of truth for
// missed events on reconnect.
type EventBus struct {
	mu        sync.RWMutex
	taskSubs  map[string][]*taskSubscriber
	fleetSubs []*fleetSubscriber
}

type taskSubscriber struct {
	ch        chan *apiv1.TaskEvent
	closed    chan struct{}
	closeOnce sync.Once
}

type fleetSubscriber struct {
	ch        chan *apiv1.FleetUpdate
	closed    chan struct{}
	closeOnce sync.Once
}

func (s *taskSubscriber) close() {
	s.closeOnce.Do(func() { close(s.closed) })
}

func (s *fleetSubscriber) close() {
	s.closeOnce.Do(func() { close(s.closed) })
}

func NewEventBus() *EventBus {
	return &EventBus{
		taskSubs: make(map[string][]*taskSubscriber),
	}
}

// PublishFleetUpdate fans out a fleet-level update (runner registered/lost)
// to all fleet subscribers.
func (b *EventBus) PublishFleetUpdate(update *apiv1.FleetUpdate) {
	if b == nil {
		return
	}

	b.mu.RLock()
	fleetCopy := make([]*fleetSubscriber, len(b.fleetSubs))
	copy(fleetCopy, b.fleetSubs)
	b.mu.RUnlock()

	for _, sub := range fleetCopy {
		sendFleetNonBlocking(sub.ch, sub.closed, update)
	}
}

// SubscribeTaskEvents registers a subscriber for a specific task's events.
// Returns a channel and an unsubscribe function. The channel is buffered
// with the given capacity. Events are dropped (non-blocking) if the
// subscriber is too slow — clients should use the DB cursor on reconnect
// to recover missed events.
func (b *EventBus) SubscribeTaskEvents(taskID string, bufSize int) (<-chan *apiv1.TaskEvent, func()) {
	if b == nil || bufSize <= 0 {
		ch := make(chan *apiv1.TaskEvent)
		return ch, func() {}
	}
	sub := &taskSubscriber{
		ch:     make(chan *apiv1.TaskEvent, bufSize),
		closed: make(chan struct{}),
	}

	b.mu.Lock()
	b.taskSubs[taskID] = append(b.taskSubs[taskID], sub)
	b.mu.Unlock()

	return sub.ch, func() {
		sub.close()
		b.mu.Lock()
		subs := b.taskSubs[taskID]
		for i, s := range subs {
			if s == sub {
				b.taskSubs[taskID] = append(subs[:i], subs[i+1:]...)
				break
			}
		}
		if len(b.taskSubs[taskID]) == 0 {
			delete(b.taskSubs, taskID)
		}
		b.mu.Unlock()
	}
}

// SubscribeFleetUpdates registers a subscriber for fleet-wide updates.
func (b *EventBus) SubscribeFleetUpdates(bufSize int) (<-chan *apiv1.FleetUpdate, func()) {
	if b == nil || bufSize <= 0 {
		ch := make(chan *apiv1.FleetUpdate)
		return ch, func() {}
	}
	sub := &fleetSubscriber{
		ch:     make(chan *apiv1.FleetUpdate, bufSize),
		closed: make(chan struct{}),
	}

	b.mu.Lock()
	b.fleetSubs = append(b.fleetSubs, sub)
	b.mu.Unlock()

	return sub.ch, func() {
		sub.close()
		b.mu.Lock()
		for i, s := range b.fleetSubs {
			if s == sub {
				b.fleetSubs = append(b.fleetSubs[:i], b.fleetSubs[i+1:]...)
				break
			}
		}
		b.mu.Unlock()
	}
}

// CloseAll closes all subscriber channels. Called on server shutdown.
func (b *EventBus) CloseAll() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, subs := range b.taskSubs {
		for _, sub := range subs {
			sub.close()
		}
	}
	for _, sub := range b.fleetSubs {
		sub.close()
	}
	b.taskSubs = make(map[string][]*taskSubscriber)
	b.fleetSubs = nil
}

func sendNonBlocking(ch chan *apiv1.TaskEvent, closed chan struct{}, event *apiv1.TaskEvent) {
	select {
	case ch <- event:
	case <-closed:
	default:
	}
}

func sendFleetNonBlocking(ch chan *apiv1.FleetUpdate, closed chan struct{}, update *apiv1.FleetUpdate) {
	select {
	case ch <- update:
	case <-closed:
	default:
	}
}

// PublishTaskEvent implements service.TaskEventPublisher using plain-string
// parameters (avoids import cycle between service and webapi packages).
func (b *EventBus) PublishTaskEvent(taskID, eventID, status, eventType, summary, payload, createdAt string) {
	if b == nil {
		return
	}
	event := &apiv1.TaskEvent{
		TaskId:    taskID,
		Id:        eventID,
		Status:    status,
		EventType: eventType,
		Payload:   payload,
		CreatedAt: createdAt,
	}
	b.publishTaskEventProto(taskID, event)
}

func (b *EventBus) publishTaskEventProto(taskID string, event *apiv1.TaskEvent) {
	if b == nil {
		return
	}

	b.mu.RLock()
	taskSubs := b.taskSubs[taskID]
	taskCopy := make([]*taskSubscriber, len(taskSubs))
	copy(taskCopy, taskSubs)
	fleetCopy := make([]*fleetSubscriber, len(b.fleetSubs))
	copy(fleetCopy, b.fleetSubs)
	b.mu.RUnlock()

	for _, sub := range taskCopy {
		sendNonBlocking(sub.ch, sub.closed, event)
	}

	if len(fleetCopy) > 0 {
		update := &apiv1.FleetUpdate{
			Type: "task_status_change",
		}
		for _, sub := range fleetCopy {
			sendFleetNonBlocking(sub.ch, sub.closed, update)
		}
	}
}
