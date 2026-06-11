package task

import (
	"encoding/json"
	"testing"
)

func TestTaskRequest_MarshalJSON(t *testing.T) {
	req := TaskRequest{
		TaskID:     "task-123",
		AgentImage: "ghcr.io/flatout-works/devfleet-runner:latest",
		Prompt:     "build me an api",
		TimeoutSec: 3600,
		MaxMemoryMB: 4096,
		MaxCPU: 2,
		Env: map[string]string{
			"LLM_PROVIDER": "synthetic",
		},
	}

	data, err := json.Marshal(&req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if parsed["task_id"] != "task-123" {
		t.Errorf("task_id = %v", parsed["task_id"])
	}
	if parsed["timeout_sec"] != float64(3600) {
		t.Errorf("timeout_sec = %v", parsed["timeout_sec"])
	}
}

func TestTaskRequest_Defaults(t *testing.T) {
	req := TaskRequest{TaskID: "t1"}
	data, _ := json.Marshal(&req)
	var parsed map[string]any
	json.Unmarshal(data, &parsed)
	if _, ok := parsed["command"]; ok {
		t.Error("empty command should be omitted")
	}
	if _, ok := parsed["git_url"]; ok {
		t.Error("empty git_url should be omitted")
	}
}

func TestTaskResponse_MarshalJSON(t *testing.T) {
	resp := TaskResponse{
		TaskID:  "task-1",
		Status:  "running",
		Summary: "everything is fine",
	}

	data, err := json.Marshal(&resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if parsed["status"] != "running" {
		t.Errorf("status = %v", parsed["status"])
	}
}

func TestReport_MarshalJSON(t *testing.T) {
	r := Report{
		Status:  "success",
		Summary: "service deployed",
		Artifacts: []string{"api/openapi.yaml"},
	}

	data, err := json.Marshal(&r)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]any
	json.Unmarshal(data, &parsed)
	if parsed["status"] != "success" {
		t.Errorf("status = %v", parsed["status"])
	}
	arts, _ := parsed["artifacts"].([]any)
	if len(arts) != 1 {
		t.Errorf("artifacts len = %d", len(arts))
	}
}
