package tools

import "testing"

func TestSafeTaskID(t *testing.T) {
	tests := []struct {
		taskID string
		want   string
	}{
		{"Task-ABC-123", "task-abc-123"},
		{"My/Project/ID", "my-project-id"},
		{"task-abc", "task-abc"},
	}
	for _, tc := range tests {
		d := &Deploy{TaskID: tc.taskID}
		got := d.safeTaskID()
		if got != tc.want {
			t.Errorf("safeTaskID(%q) = %q, want %q", tc.taskID, got, tc.want)
		}
	}
}

func TestImageBase(t *testing.T) {
	t.Run("with registry", func(t *testing.T) {
		d := &Deploy{TaskID: "Task-ABC", Registry: "ghcr.io"}
		got := d.imageBase()
		want := "ghcr.io/devfleet-task-abc"
		if got != want {
			t.Errorf("imageBase() = %q, want %q", got, want)
		}
	})
	t.Run("without registry", func(t *testing.T) {
		d := &Deploy{TaskID: "Task-ABC", Registry: ""}
		got := d.imageBase()
		want := "devfleet-task-abc"
		if got != want {
			t.Errorf("imageBase() = %q, want %q", got, want)
		}
	})
}

func TestImageTag(t *testing.T) {
	d := &Deploy{TaskID: "Task-ABC"}
	got := d.imageTag()
	want := "devfleet-task-abc:latest"
	if got != want {
		t.Errorf("imageTag() = %q, want %q", got, want)
	}
}

func TestContainerName(t *testing.T) {
	d := &Deploy{TaskID: "Task-ABC"}
	got := d.containerName()
	want := "devfleet-task-abc"
	if got != want {
		t.Errorf("containerName() = %q, want %q", got, want)
	}
}

func TestResolveContainerName(t *testing.T) {
	t.Run("with name in args", func(t *testing.T) {
		d := &Deploy{TaskID: "Task-ABC"}
		args := map[string]any{"name": "my-container"}
		got := d.resolveContainerName(args)
		if got != "my-container" {
			t.Errorf("resolveContainerName() = %q, want %q", got, "my-container")
		}
	})
	t.Run("without name in args", func(t *testing.T) {
		d := &Deploy{TaskID: "Task-ABC"}
		args := map[string]any{}
		got := d.resolveContainerName(args)
		want := d.containerName()
		if got != want {
			t.Errorf("resolveContainerName() = %q, want %q", got, want)
		}
	})
	t.Run("with empty name in args", func(t *testing.T) {
		d := &Deploy{TaskID: "Task-ABC"}
		args := map[string]any{"name": ""}
		got := d.resolveContainerName(args)
		want := d.containerName()
		if got != want {
			t.Errorf("resolveContainerName() = %q, want %q", got, want)
		}
	})
}

func TestResolvePort(t *testing.T) {
	t.Run("with port in args", func(t *testing.T) {
		d := &Deploy{TaskID: "Task-ABC"}
		args := map[string]any{"port": "3000"}
		got := d.resolvePort(args)
		if got != "3000" {
			t.Errorf("resolvePort() = %q, want %q", got, "3000")
		}
	})
	t.Run("without port", func(t *testing.T) {
		d := &Deploy{TaskID: "Task-ABC"}
		args := map[string]any{}
		got := d.resolvePort(args)
		if got != defaultDeployPort {
			t.Errorf("resolvePort() = %q, want %q", got, defaultDeployPort)
		}
	})
	t.Run("with empty port", func(t *testing.T) {
		d := &Deploy{TaskID: "Task-ABC"}
		args := map[string]any{"port": ""}
		got := d.resolvePort(args)
		if got != defaultDeployPort {
			t.Errorf("resolvePort() = %q, want %q", got, defaultDeployPort)
		}
	})
}

func TestNewDeploy(t *testing.T) {
	d := NewDeploy("/tmp", DeployProviderLocal, "Task-1", "ghcr.io", "http://devfleet.local")
	if d.BaseDir != "/tmp" {
		t.Errorf("BaseDir = %q, want /tmp", d.BaseDir)
	}
	if d.Provider != DeployProviderLocal {
		t.Errorf("Provider = %q, want %q", d.Provider, DeployProviderLocal)
	}
	if d.TaskID != "Task-1" {
		t.Errorf("TaskID = %q, want Task-1", d.TaskID)
	}
	if d.Registry != "ghcr.io" {
		t.Errorf("Registry = %q, want ghcr.io", d.Registry)
	}
	if d.DevfleetURL != "http://devfleet.local" {
		t.Errorf("DevfleetURL = %q, want http://devfleet.local", d.DevfleetURL)
	}
}
