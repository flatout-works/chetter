package opencode

import "testing"

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
			want:      true,
		},
		{
			name:      "completed type",
			props:     map[string]any{"type": "completed"},
			sessionID: "ses_123",
			want:      true,
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
				"type":       "idle",
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
			want:      true,
		},
		{
			name:      "id field matches session",
			props:     map[string]any{"id": "ses_123", "type": "finished"},
			sessionID: "ses_123",
			want:      true,
		},
		{
			name:      "no sessionID in props matches any session",
			props:     map[string]any{"type": "done"},
			sessionID: "ses_123",
			want:      true,
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
