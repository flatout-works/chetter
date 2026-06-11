package containerd

import (
	"slices"
	"testing"
)

func TestRunKataArgsIncludesNetworkNamespaceAndCommand(t *testing.T) {
	c := NewClient("test-ns")
	args := c.runKataArgs(
		"task-1",
		"docker.io/library/alpine:latest",
		[]Mount{
			{Type: "bind", Source: "/host", Destination: "/workspace", Options: []string{"rbind", "rw"}},
			{Type: "bind", Source: "/host/resolv.conf", Destination: "/etc/resolv.conf", Options: []string{"rbind", "ro"}},
		},
		map[string]string{"TASK_ID": "task-1"},
		"/run/netns/fo-task",
		[]string{"/bin/sh", "-c", "echo ok"},
	)

	if !slices.Contains(args, "network:/run/netns/fo-task") {
		t.Fatalf("args missing network namespace: %v", args)
	}
	if !slices.Contains(args, "--mount") {
		t.Fatalf("args missing mount flag: %v", args)
	}
	if !slices.Contains(args, "type=bind,src=/host/resolv.conf,dst=/etc/resolv.conf,options=rbind:ro") {
		t.Fatalf("args missing resolv.conf mount: %v", args)
	}
	if !slices.Contains(args, "TASK_ID=task-1") {
		t.Fatalf("args missing env flag: %v", args)
	}

	wantSuffix := []string{"docker.io/library/alpine:latest", "task-1", "/bin/sh", "-c", "echo ok"}
	if len(args) < len(wantSuffix) {
		t.Fatalf("args too short: %v", args)
	}
	gotSuffix := args[len(args)-len(wantSuffix):]
	if !slices.Equal(gotSuffix, wantSuffix) {
		t.Fatalf("args suffix = %v, want %v", gotSuffix, wantSuffix)
	}
}
