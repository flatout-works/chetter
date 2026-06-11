package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/flatout-works/devfleet/runner/internal/nats"
)

// NatsTool provides MCP tool handlers for NATS publish and request,
// allowing agents to interact with the control plane.
type NatsTool struct {
	Client *nats.Client
}

// Publish handles nats_publish.
func (n *NatsTool) Publish(ctx context.Context, args map[string]any) (any, error) {
	subject, err := requireString(args, "subject")
	if err != nil {
		return nil, err
	}
	payload := getOptString(args, "payload", "")
	if err := n.Client.Publish(subject, []byte(payload)); err != nil {
		return nil, fmt.Errorf("nats publish: %w", err)
	}
	return "published", nil
}

// Request handles nats_request.
func (n *NatsTool) Request(ctx context.Context, args map[string]any) (any, error) {
	subject, err := requireString(args, "subject")
	if err != nil {
		return nil, err
	}
	payload := getOptString(args, "payload", "")
	timeoutSec := getOptFloat64(args, "timeout_sec", 30)
	if timeoutSec == 0 {
		timeoutSec = 30
	}
	msg, err := n.Client.Request(subject, []byte(payload), time.Duration(timeoutSec)*time.Second)
	if err != nil {
		return nil, fmt.Errorf("nats request: %w", err)
	}
	return string(msg.Data), nil
}
