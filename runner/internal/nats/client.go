// Package nats wraps the NATS client library with connection, publish,
// request, and subscribe convenience methods.
package nats

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// Client wraps a NATS connection with simplified publish, request, and
// subscribe methods.
type Client struct {
	Conn *nats.Conn
	JS   nats.JetStreamContext
	URL  string
}

// Connect establishes a NATS connection with auto-reconnect and JetStream
// context refresh on reconnection.
func Connect(url string) (*Client, error) {
	client := &Client{URL: url}
	nc, err := nats.Connect(url,
		nats.Timeout(10*time.Second),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			if client.JS != nil {
				if err := client.RefreshJetStream(); err != nil {
					slog.Warn("jetstream context refresh after reconnect failed", "err", err)
				} else {
					slog.Info("jetstream context refreshed after reconnect")
				}
			}
		}),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			slog.Warn("nats disconnected", "err", err)
		}),
		nats.ClosedHandler(func(_ *nats.Conn) {
			slog.Warn("nats connection closed permanently")
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}
	client.Conn = nc
	slog.Info("connected", "component", "nats", "url", url)
	return client, nil
}

// Publish sends a message to a subject. When JetStream is enabled,
// it attempts a JS publish first and falls back to a core NATS publish
// if the JS context is stale (e.g. after a reconnect).
func (c *Client) Publish(subject string, data []byte) error {
	if c.JS != nil {
		if _, err := c.JS.Publish(subject, data); err == nil {
			return nil
		}
	}
	return c.Conn.Publish(subject, data)
}

// RefreshJetStream obtains a fresh JetStream context from the current
// NATS connection. Use this after the connection has reconnected to
// replace a stale JS context.
func (c *Client) RefreshJetStream() error {
	js, err := c.Conn.JetStream()
	if err != nil {
		return fmt.Errorf("jetstream context refresh: %w", err)
	}
	c.JS = js
	return nil
}

// Request does a request-response.
func (c *Client) Request(subject string, data []byte, timeout time.Duration) (*nats.Msg, error) {
	return c.Conn.Request(subject, data, timeout)
}

// Subscribe subscribes to a subject.
func (c *Client) Subscribe(subject string, cb nats.MsgHandler) (*nats.Subscription, error) {
	return c.Conn.Subscribe(subject, cb)
}

// EnableJetStream initializes a JetStream context and ensures the runner's
// task and event streams exist. It is opt-in so local development can keep
// using plain NATS core subjects.
func (c *Client) EnableJetStream(taskStream, eventStream, taskSubject string, eventSubjects []string, storage string) error {
	js, err := c.Conn.JetStream()
	if err != nil {
		return fmt.Errorf("jetstream context: %w", err)
	}
	c.JS = js

	storageType := nats.FileStorage
	if storage == "memory" {
		storageType = nats.MemoryStorage
	}
	if err := ensureStream(js, taskStream, []string{taskSubject}, storageType, nats.WorkQueuePolicy); err != nil {
		return err
	}
	if err := ensureStream(js, eventStream, eventSubjects, storageType, nats.LimitsPolicy); err != nil {
		return err
	}
	return nil
}

// QueueSubscribeManualAck subscribes through a durable JetStream queue
// consumer. Callers must Ack terminal task messages after processing.
func (c *Client) QueueSubscribeManualAck(subject, queue, durable string, ackWait time.Duration, maxDeliver, maxAckPending int, cb nats.MsgHandler) (*nats.Subscription, error) {
	if c.JS == nil {
		return nil, fmt.Errorf("jetstream is not enabled")
	}
	opts := manualAckQueueOptions(durable, ackWait, maxDeliver, maxAckPending)
	sub, err := c.JS.QueueSubscribe(subject, queue, cb, opts...)
	if err == nil || !isConsumerConfigMismatch(err) || durable == "" {
		return sub, err
	}

	if syncErr := c.updateDurableConsumerLimits(subject, durable, ackWait, maxDeliver, maxAckPending); syncErr != nil {
		slog.Warn("could not update existing durable consumer config; binding with existing config", "durable", durable, "err", syncErr)
		return c.bindExistingDurableQueue(subject, queue, durable, cb)
	}
	return c.JS.QueueSubscribe(subject, queue, cb, opts...)
}

func manualAckQueueOptions(durable string, ackWait time.Duration, maxDeliver, maxAckPending int) []nats.SubOpt {
	opts := []nats.SubOpt{
		nats.Durable(durable),
		nats.ManualAck(),
		nats.AckWait(ackWait),
		nats.MaxDeliver(maxDeliver),
	}
	if maxAckPending > 0 {
		opts = append(opts, nats.MaxAckPending(maxAckPending))
	}
	return opts
}

func isConsumerConfigMismatch(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "configuration requests") && strings.Contains(msg, "consumer's value")
}

func (c *Client) updateDurableConsumerLimits(subject, durable string, ackWait time.Duration, maxDeliver, maxAckPending int) error {
	stream, err := c.JS.StreamNameBySubject(subject)
	if err != nil {
		return fmt.Errorf("stream by subject %s: %w", subject, err)
	}
	info, err := c.JS.ConsumerInfo(stream, durable)
	if err != nil {
		return fmt.Errorf("consumer info %s/%s: %w", stream, durable, err)
	}
	cfg := info.Config
	cfg.AckWait = ackWait
	cfg.MaxDeliver = maxDeliver
	if maxAckPending > 0 {
		cfg.MaxAckPending = maxAckPending
	}
	if _, err := c.JS.UpdateConsumer(stream, &cfg); err != nil {
		return fmt.Errorf("update consumer %s/%s: %w", stream, durable, err)
	}
	slog.Info("updated durable consumer limits", "stream", stream, "durable", durable, "ackWait", ackWait, "maxDeliver", maxDeliver, "maxAckPending", maxAckPending)
	return nil
}

func (c *Client) bindExistingDurableQueue(subject, queue, durable string, cb nats.MsgHandler) (*nats.Subscription, error) {
	stream, err := c.JS.StreamNameBySubject(subject)
	if err != nil {
		return nil, fmt.Errorf("stream by subject %s: %w", subject, err)
	}
	return c.JS.QueueSubscribe(subject, queue, cb, nats.Bind(stream, durable), nats.ManualAck())
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
