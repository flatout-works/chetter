package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsSessionIdleStatus(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		props     map[string]any
		sessionID string
		want      bool
	}{
		{
			name:      "idle type flat",
			props:     map[string]any{"type": "idle"},
			sessionID: "ses_123",
			want:      false,
		},
		{
			name:      "completed type",
			props:     map[string]any{"type": "completed"},
			sessionID: "ses_123",
			want:      false,
		},
		{
			name:      "busy type",
			props:     map[string]any{"type": "busy"},
			sessionID: "ses_123",
			want:      false,
		},
		{
			name: "nested status map idle",
			props: map[string]any{
				"sessionID": "ses_123",
				"status":    map[string]any{"type": "idle"},
			},
			sessionID: "ses_123",
			want:      true,
		},
		{
			name: "nested status map busy",
			props: map[string]any{
				"sessionID": "ses_123",
				"status":    map[string]any{"type": "busy"},
			},
			sessionID: "ses_123",
			want:      false,
		},
		{
			name: "different session ID",
			props: map[string]any{
				"sessionID": "ses_other",
				"type":      "idle",
			},
			sessionID: "ses_123",
			want:      false,
		},
		{
			name: "different event ID",
			props: map[string]any{
				"id":   "ses_other",
				"type": "idle",
			},
			sessionID: "ses_123",
			want:      false,
		},
		{
			name:      "nil props",
			props:     nil,
			sessionID: "ses_123",
			want:      false,
		},
		{
			name:      "status as string",
			props:     map[string]any{"status": "idle"},
			sessionID: "ses_123",
			want:      false,
		},
		{
			name:      "id field matches session",
			props:     map[string]any{"id": "ses_123", "type": "finished"},
			sessionID: "ses_123",
			want:      true,
		},
		{
			name:      "no sessionID does not complete a session",
			props:     map[string]any{"type": "done"},
			sessionID: "ses_123",
			want:      false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isSessionIdleStatus(tc.props, tc.sessionID); got != tc.want {
				t.Fatalf("isSessionIdleStatus() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsSessionIdleEvent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		props     map[string]any
		sessionID string
		want      bool
	}{
		{
			name:      "matching session",
			props:     map[string]any{"sessionID": "ses_123"},
			sessionID: "ses_123",
			want:      true,
		},
		{
			name:      "missing session ID",
			props:     map[string]any{},
			sessionID: "ses_123",
			want:      false,
		},
		{
			name:      "different session",
			props:     map[string]any{"sessionID": "ses_other"},
			sessionID: "ses_123",
			want:      false,
		},
		{
			name:      "nil properties",
			sessionID: "ses_123",
			want:      false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isSessionIdleEvent(tc.props, tc.sessionID); got != tc.want {
				t.Fatalf("isSessionIdleEvent() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWatchEventsSignalsTerminalEvents(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		event string
	}{
		{
			name:  "session idle",
			event: `{"type":"session.idle","properties":{"sessionID":"ses_123"}}`,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if _, err := w.Write([]byte("data: " + tc.event + "\n\n")); err != nil {
					t.Errorf("write event: %v", err)
				}
			}))
			defer server.Close()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			idleCh := make(chan struct{}, 1)
			done := make(chan struct{})
			go func() {
				watchEvents(ctx, "task", server.URL, "", func(string, string) {}, nil, "ses_123", func() {
					select {
					case idleCh <- struct{}{}:
					default:
					}
				})
				close(done)
			}()

			select {
			case <-idleCh:
				cancel()
			case <-time.After(2 * time.Second):
				t.Fatal("timed out waiting for completion signal")
			}

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("event watcher did not stop")
			}
		})
	}
}

func TestWatchEventsDoesNotCompleteOnAssistantStop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(`data: {"type":"message.updated","properties":{"info":{"role":"assistant","sessionID":"ses_123","finish":"stop"}}}` + "\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	idle := make(chan struct{})
	done := make(chan struct{})
	go func() {
		watchEvents(ctx, "task", server.URL, "", func(string, string) {}, nil, "ses_123", func() {
			close(idle)
		})
		close(done)
	}()

	select {
	case <-idle:
		t.Fatal("assistant message stop must not complete the session")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event watcher did not stop")
	}
}
