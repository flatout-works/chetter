// Package transport contains protocol-independent HTTP and SSE helpers shared
// by the runner's HTTP-backed harness adapters.
package transport

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	readyPollInterval = 500 * time.Millisecond
	readyHTTPTimeout  = 2 * time.Second
)

// Authenticator applies harness-specific authentication to an HTTP request.
type Authenticator func(*http.Request)

// WaitForReady polls endpoint until it returns a successful HTTP status or the
// timeout/context expires.
func WaitForReady(ctx context.Context, baseURL, endpoint string, auth Authenticator, timeout time.Duration, label string) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(readyPollInterval)
	defer ticker.Stop()
	client := &http.Client{Timeout: readyHTTPTimeout}
	var lastErr error
	var lastStatus int

	check := func() {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+endpoint, nil)
		if err != nil {
			lastErr = err
			return
		}
		if auth != nil {
			auth(req)
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			lastErr = nil
			lastStatus = resp.StatusCode
			return
		}
		lastStatus = resp.StatusCode
	}

	for {
		check()
		if lastErr == nil && lastStatus >= 200 && lastStatus < 300 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			if lastStatus >= 400 {
				return fmt.Errorf("%s at %s not ready within %v: last status: %d", label, baseURL, timeout, lastStatus)
			}
			if lastErr != nil {
				return fmt.Errorf("%s at %s not ready within %v: last error: %w", label, baseURL, timeout, lastErr)
			}
			return fmt.Errorf("%s at %s not ready within %v", label, baseURL, timeout)
		case <-ticker.C:
		}
	}
}

// Event is one server-sent event frame.
type Event struct {
	Type string
	Data string
}

// EventReader decodes SSE event and data fields, including multi-line data.
type EventReader struct {
	br *bufio.Reader
}

// NewEventReader creates an SSE reader over r.
func NewEventReader(r io.Reader) *EventReader {
	return &EventReader{br: bufio.NewReader(r)}
}

// Read returns the next complete SSE event.
func (r *EventReader) Read() (*Event, error) {
	var event Event
	for {
		line, err := r.br.ReadString('\n')
		if len(line) > 0 {
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if event.Type != "" || event.Data != "" {
					return &event, nil
				}
			} else {
				switch {
				case strings.HasPrefix(line, "event: "):
					event.Type = strings.TrimPrefix(line, "event: ")
				case strings.HasPrefix(line, "data: "):
					if event.Data != "" {
						event.Data += "\n"
					}
					event.Data += strings.TrimPrefix(line, "data: ")
				}
			}
		}
		if err != nil {
			if event.Type != "" || event.Data != "" {
				return &event, nil
			}
			return nil, err
		}
	}
}
