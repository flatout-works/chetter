//go:build ignore
// +build ignore

// test_publish publishes a single task request to a running runner for
// manual testing.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/flatout-works/devfleet/runner/internal/task"
	"github.com/flatout-works/devfleet/runner/test/testutil"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run test_publish.go <task-id>")
		os.Exit(1)
	}

	nc := testutil.ConnectNATS()
	defer nc.Close()

	req := task.TaskRequest{
		TaskID:     os.Args[1],
		AgentImage: "docker.io/library/alpine:latest",
		GitURL:     "https://github.com/example/myapp",
		TimeoutSec: 60,
	}

	statusSubject := fmt.Sprintf(testutil.DefaultStatusFmt, req.TaskID)
	_, sub, err := testutil.PublishAndListen(nc, req, testutil.DefaultTasksSubject, statusSubject)
	if err != nil {
		fmt.Fprintf(os.Stderr, "publish: %v\n", err)
		os.Exit(1)
	}
	defer sub.Unsubscribe()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	fmt.Println("shutting down...")
}
