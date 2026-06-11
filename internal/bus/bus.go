// Package bus wraps NATS and JetStream for devfleet task transport.
package bus

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/flatout-works/devfleet/internal/config"
	"github.com/nats-io/nats.go"
)

// Client owns the NATS connection and optional JetStream context.
type Client struct {
	cfg  config.Config
	conn *nats.Conn
	js   nats.JetStreamContext
}

// ClearTaskQueueResult describes the NATS side of a task queue clear.
type ClearTaskQueueResult struct {
	Stream         string `json:"stream"`
	Subject        string `json:"subject"`
	MessagesBefore uint64 `json:"messages_before"`
	MessagesAfter  uint64 `json:"messages_after"`
	ConsumerReset  bool   `json:"consumer_reset"`
}

// Connect opens a NATS connection and configures JetStream when enabled.
const (
	natsConnectTimeout = 10 * time.Second
	eventAckWait       = 30 * time.Second
	maxEventDeliveries = 5
)

func Connect(cfg config.Config) (*Client, error) {
	conn, err := nats.Connect(cfg.NATSURL, nats.Timeout(natsConnectTimeout))
	if err != nil {
		return nil, fmt.Errorf("connect nats: %w", err)
	}
	client := &Client{cfg: cfg, conn: conn}
	if cfg.JetStreamEnabled {
		js, err := conn.JetStream()
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("jetstream context: %w", err)
		}
		client.js = js
		if err := client.ensureStreams(); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return client, nil
}

// PublishCancel sends a cancellation notification for the given task ID
// on the pattern devfleet.tasks.<taskID>.cancel. Runners subscribe to
// devfleet.tasks.>.cancel via a push subscription.
func (c *Client) PublishCancel(taskID string) error {
	prefix := strings.TrimSuffix(c.cfg.EventSubject, ">")
	subject := prefix + taskID + ".cancel"
	payload := []byte(`{"task_id":"` + taskID + `"}`)
	if err := c.conn.Publish(subject, payload); err != nil {
		return fmt.Errorf("publish cancel %q: %w", subject, err)
	}
	return c.conn.Flush()
}

// Close closes the underlying NATS connection.
func (c *Client) Close() {
	c.conn.Close()
}

// PublishTask publishes a task request to the runner task subject.
func (c *Client) PublishTask(data []byte) error {
	if c.js != nil {
		if _, err := c.js.Publish(c.cfg.TaskSubject, data); err != nil {
			return fmt.Errorf("jetstream publish %q: %w", c.cfg.TaskSubject, err)
		}
		return nil
	}
	if err := c.conn.Publish(c.cfg.TaskSubject, data); err != nil {
		return fmt.Errorf("nats publish %q: %w", c.cfg.TaskSubject, err)
	}
	return c.conn.Flush()
}

// ClearTaskQueue purges queued task messages and optionally resets the durable
// task consumer so stuck ack-pending deliveries do not block new runners.
func (c *Client) ClearTaskQueue(preserveConsumer bool) (ClearTaskQueueResult, error) {
	result := ClearTaskQueueResult{Stream: c.cfg.TaskStream, Subject: c.cfg.TaskSubject}
	if c.js == nil {
		return result, fmt.Errorf("clearing task queue requires JetStream")
	}
	info, err := c.js.StreamInfo(c.cfg.TaskStream)
	if err != nil {
		return result, fmt.Errorf("task stream info before purge: %w", err)
	}
	result.MessagesBefore = info.State.Msgs
	if err := c.js.PurgeStream(c.cfg.TaskStream, &nats.StreamPurgeRequest{Subject: c.cfg.TaskSubject}); err != nil {
		return result, fmt.Errorf("purge task stream %q: %w", c.cfg.TaskStream, err)
	}
	if !preserveConsumer && c.cfg.TaskDurable != "" {
		if err := c.js.DeleteConsumer(c.cfg.TaskStream, c.cfg.TaskDurable); err != nil {
			if !errors.Is(err, nats.ErrConsumerNotFound) {
				return result, fmt.Errorf("delete task consumer %q: %w", c.cfg.TaskDurable, err)
			}
		} else {
			result.ConsumerReset = true
		}
	}
	info, err = c.js.StreamInfo(c.cfg.TaskStream)
	if err != nil {
		return result, fmt.Errorf("task stream info after purge: %w", err)
	}
	result.MessagesAfter = info.State.Msgs
	return result, nil
}

// SubscribeEvents subscribes to runner status events. JetStream subscriptions
// are manual-ack because status events are persisted in TiDB before acking.
func (c *Client) SubscribeEvents(cb nats.MsgHandler) (*nats.Subscription, error) {
	if c.js != nil {
		return c.js.QueueSubscribe(c.cfg.EventSubject, c.cfg.EventQueue, cb,
			nats.Durable(c.cfg.EventDurable),
			nats.ManualAck(),
			nats.AckWait(eventAckWait),
			nats.MaxDeliver(maxEventDeliveries),
		)
	}
	return c.conn.Subscribe(c.cfg.EventSubject, cb)
}

func (c *Client) ensureStreams() error {
	storage := nats.FileStorage
	if c.cfg.Storage == "memory" {
		storage = nats.MemoryStorage
	}
	if err := ensureStream(c.js, c.cfg.TaskStream, []string{c.cfg.TaskSubject}, storage, nats.WorkQueuePolicy); err != nil {
		return fmt.Errorf("ensure task stream %q: %w", c.cfg.TaskStream, err)
	}
	if err := ensureStream(c.js, c.cfg.EventStream, []string{c.cfg.EventSubject}, storage, nats.LimitsPolicy); err != nil {
		return fmt.Errorf("ensure event stream %q: %w", c.cfg.EventStream, err)
	}
	return nil
}

func ensureStream(js nats.JetStreamContext, name string, subjects []string, storage nats.StorageType, retention nats.RetentionPolicy) error {
	info, err := js.StreamInfo(name)
	if err == nil && info != nil {
		return nil
	}
	if err != nil && err != nats.ErrStreamNotFound {
		return fmt.Errorf("stream info %s: %w", name, err)
	}
	_, err = js.AddStream(&nats.StreamConfig{
		Name:      name,
		Subjects:  subjects,
		Storage:   storage,
		Retention: retention,
	})
	if err != nil {
		return fmt.Errorf("add stream %s: %w", name, err)
	}
	return nil
}
