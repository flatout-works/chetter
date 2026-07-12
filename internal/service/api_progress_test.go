package service

import "testing"

func TestHumanProgressSummary(t *testing.T) {
	tests := []struct {
		name    string
		summary string
		want    string
	}{
		{
			name:    "passes through normal summary",
			summary: "Sending prompt to agent...",
			want:    "Sending prompt to agent...",
		},
		{
			name:    "server connected",
			summary: "opencode: server.connected",
			want:    "Connected to the agent runtime",
		},
		{
			name:    "heartbeat hidden",
			summary: "opencode: server.heartbeat",
			want:    "",
		},
		{
			name:    "busy status",
			summary: `opencode: session.status {"sessionID":"ses_123","status":{"type":"busy"}}`,
			want:    "Agent is working",
		},
		{
			name:    "step finish",
			summary: `opencode: message.part.updated {"part":{"reason":"tool-calls","tokens":{"total":11546},"type":"step-finish"}}`,
			want:    "Finished tool call step (11,546 tokens)",
		},
		{
			name:    "tool running with target",
			summary: `opencode: message.part.updated {"part":{"state":{"input":{"filePath":"/workspace/resume-fast-resumed.txt"},"status":"running"},"tool":"write","type":"tool"}}`,
			want:    "Running write on /workspace/resume-fast-resumed.txt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := humanProgressSummary(tc.summary); got != tc.want {
				t.Fatalf("humanProgressSummary() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestIsProgressHeartbeat(t *testing.T) {
	if !isProgressHeartbeat(`opencode: server.heartbeat {"task_id":"task_123"}`) {
		t.Fatal("heartbeat with payload should be hidden")
	}
	if isProgressHeartbeat(`opencode: session.status {"status":{"type":"busy"}}`) {
		t.Fatal("non-heartbeat event should not be hidden")
	}
}

func TestIsNoiseSummary(t *testing.T) {
	tests := []struct {
		name    string
		summary string
		want    bool
	}{
		{"empty", "", true},
		{"pi empty", "pi: ", true},
		{"pi colon only", "pi:", true},
		{"pi single char", "pi: .", true},
		{"pi single word", "pi: the", true},
		{"pi tool read", "pi: tool: read", false},
		{"pi tool error", "pi: tool error: bash", false},
		{"pi retrying", "pi: retrying: rate limit exceeded", false},
		{"agent session updated", "Agent session updated", true},
		{"agent message updated", "Agent message updated", true},
		{"agent updated progress", "Agent updated progress", true},
		{"agent is working", "Agent is working", false},
		{"agent is waiting", "Agent is waiting", false},
		{"connected", "Connected to the agent runtime", false},
		{"running tool", "Running read on /workspace/foo.go", false},
		{"finished step", "Finished tool call step (34,015 tokens)", false},
		{"agent replied", "Agent replied: Done. PR #164", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isNoiseSummary(tc.summary); got != tc.want {
				t.Fatalf("isNoiseSummary(%q) = %v, want %v", tc.summary, got, tc.want)
			}
		})
	}
}
