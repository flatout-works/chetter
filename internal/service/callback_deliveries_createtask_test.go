package service

import (
	"errors"
	"strings"
	"testing"
)

// TestCallbackDeliveryTaskIDDeterministic verifies the issue #405 AC3
// idempotency primitive: the child task id is a pure function of the delivery
// row id, so a replay after a crash targets the same primary key.
func TestCallbackDeliveryTaskIDDeterministic(t *testing.T) {
	first := callbackDeliveryTaskID("cbd_replay")
	second := callbackDeliveryTaskID("cbd_replay")
	if first != second {
		t.Fatalf("callbackDeliveryTaskID is not deterministic: %q != %q", first, second)
	}
	if !strings.HasPrefix(first, "task_") {
		t.Fatalf("callbackDeliveryTaskID(%q) = %q, want task_ prefix", "cbd_replay", first)
	}
	if other := callbackDeliveryTaskID("cbd_other"); other == first {
		t.Fatalf("distinct deliveries produced the same child id %q", first)
	}
}

// TestCallbackDeliveryTerminalError ensures the terminal marker used to
// dead-letter create_task config/template failures survives wrapping.
func TestCallbackDeliveryTerminalError(t *testing.T) {
	err := terminalCallbackDeliveryError("parse create_task delivery snapshot: %v", errors.New("bad json"))
	var target *callbackDeliveryTerminalError
	if !errors.As(err, &target) {
		t.Fatalf("terminalCallbackDeliveryError does not unwrap to *callbackDeliveryTerminalError: %v", err)
	}
	if !strings.Contains(err.Error(), "bad json") {
		t.Fatalf("terminal error lost its cause: %v", err)
	}
}
