package webapi

import (
	"sync"
	"testing"
	"time"

	apiv1 "github.com/flatout-works/chetter/gen/proto/api/v1"
)

func TestEventBusSubscribeTaskEvents(t *testing.T) {
	bus := NewEventBus()

	ch, unsub := bus.SubscribeTaskEvents("task_1", 10)
	defer unsub()

	bus.PublishTaskEvent("task_1", "evt_1", "running", "started", `{"summary":"hello"}`, "2025-01-01T00:00:00Z")

	select {
	case ev := <-ch:
		if ev.Id != "evt_1" {
			t.Errorf("event id = %q, want %q", ev.Id, "evt_1")
		}
		if ev.Status != "running" {
			t.Errorf("status = %q, want %q", ev.Status, "running")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBusSubscribeTaskEventsOnlyReceivesOwnTask(t *testing.T) {
	bus := NewEventBus()

	ch1, unsub1 := bus.SubscribeTaskEvents("task_a", 10)
	defer unsub1()

	bus.PublishTaskEvent("task_b", "evt_b", "done", "", "", "2025-01-01T00:00:00Z")

	select {
	case <-ch1:
		t.Fatal("should not receive event for task_b")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestEventBusUnsubscribeStopsDelivery(t *testing.T) {
	bus := NewEventBus()

	ch, unsub := bus.SubscribeTaskEvents("task_1", 10)
	unsub()

	bus.PublishTaskEvent("task_1", "evt_2", "done", "", "", "2025-01-01T00:00:00Z")

	select {
	case <-ch:
		t.Fatal("should not receive event after unsubscribe")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestEventBusMultipleSubscribers(t *testing.T) {
	bus := NewEventBus()

	ch1, unsub1 := bus.SubscribeTaskEvents("task_1", 10)
	defer unsub1()
	ch2, unsub2 := bus.SubscribeTaskEvents("task_1", 10)
	defer unsub2()

	bus.PublishTaskEvent("task_1", "evt_1", "done", "", "", "2025-01-01T00:00:00Z")

	var wg sync.WaitGroup
	wg.Add(2)
	received := make([]bool, 2)

	go func() {
		select {
		case <-ch1:
			received[0] = true
		case <-time.After(time.Second):
		}
		wg.Done()
	}()
	go func() {
		select {
		case <-ch2:
			received[1] = true
		case <-time.After(time.Second):
		}
		wg.Done()
	}()
	wg.Wait()

	if !received[0] {
		t.Error("subscriber 1 did not receive event")
	}
	if !received[1] {
		t.Error("subscriber 2 did not receive event")
	}
}

func TestEventBusFleetSubscribeAndPublish(t *testing.T) {
	bus := NewEventBus()

	ch, unsub := bus.SubscribeFleetUpdates(10)
	defer unsub()

	bus.PublishFleetUpdate(&apiv1.FleetUpdate{
		Type: "runner_registered",
	})

	select {
	case update := <-ch:
		if update.Type != "runner_registered" {
			t.Errorf("fleet update type = %q, want %q", update.Type, "runner_registered")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for fleet update")
	}
}

func TestEventBusFleetUpdateIsNonBlocking(t *testing.T) {
	bus := NewEventBus()

	ch, unsub := bus.SubscribeFleetUpdates(1)
	defer unsub()

	// Fill the buffer
	bus.PublishFleetUpdate(&apiv1.FleetUpdate{Type: "first"})

	// This would block if the channel is full — should not
	bus.PublishFleetUpdate(&apiv1.FleetUpdate{Type: "second"})

	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("should have received at least one update")
	}
}

func TestEventBusCloseAllStopsDelivery(t *testing.T) {
	bus := NewEventBus()

	ch1, _ := bus.SubscribeTaskEvents("task_1", 10)
	ch2, _ := bus.SubscribeFleetUpdates(10)

	bus.CloseAll()

	// Publishing after CloseAll should not panic
	bus.PublishTaskEvent("task_1", "evt_3", "done", "", "", "")
	bus.PublishFleetUpdate(&apiv1.FleetUpdate{Type: "test"})

	// After CloseAll, subscribers should NOT receive new events
	// (sendNonBlocking/sendFleetNonBlocking see closed channel and skip)
	select {
	case <-ch1:
		t.Fatal("should not receive event after CloseAll")
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-ch2:
		t.Fatal("should not receive update after CloseAll")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestEventBusPublishNilBus(t *testing.T) {
	var bus *EventBus = nil

	// These should not panic
	bus.PublishTaskEvent("task_1", "evt_1", "done", "", "", "")
	bus.PublishFleetUpdate(&apiv1.FleetUpdate{Type: "test"})
	bus.CloseAll()

	ch, unsub := bus.SubscribeTaskEvents("task_1", 10)
	unsub()
	ch2, unsub2 := bus.SubscribeFleetUpdates(10)
	unsub2()

	// Channels returned by nil bus should work (they're buffered channels)
	select {
	case <-ch:
	case <-time.After(10 * time.Millisecond):
	}
	select {
	case <-ch2:
	case <-time.After(10 * time.Millisecond):
	}
}

func TestEventBusTaskEventFansOutToFleetSubscribers(t *testing.T) {
	bus := NewEventBus()

	fleetCh, unsub := bus.SubscribeFleetUpdates(10)
	defer unsub()

	taskCh, unsubTask := bus.SubscribeTaskEvents("task_x", 10)
	defer unsubTask()

	bus.PublishTaskEvent("task_x", "evt_1", "done", "summary", `{}`, "2025-01-01T00:00:00Z")

	// Task subscriber should get the event
	select {
	case ev := <-taskCh:
		if ev.TaskId != "task_x" {
			t.Errorf("task event TaskId = %q, want %q", ev.TaskId, "task_x")
		}
	case <-time.After(time.Second):
		t.Fatal("task subscriber did not receive event")
	}

	// Fleet subscriber should also get notified
	select {
	case update := <-fleetCh:
		if update.Type != "task_status_change" {
			t.Errorf("fleet update type = %q, want %q", update.Type, "task_status_change")
		}
	case <-time.After(time.Second):
		t.Fatal("fleet subscriber did not receive update")
	}
}
