//go:build ignore
// +build ignore

// wait_for_done publishes a task and blocks until the runner reports a
// terminal status.
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/flatout-works/devfleet/runner/internal/task"
	"github.com/flatout-works/devfleet/runner/test/testutil"
)

func main() {
	nc := testutil.ConnectNATS()
	defer nc.Close()

	taskID := fmt.Sprintf("done-test-%d", time.Now().Unix())
	req := task.TaskRequest{
		TaskID:     taskID,
		AgentImage: "docker.io/library/alpine:latest",
		TimeoutSec: 30,
	}

	resp, err := testutil.RunTaskAndWait(nc, req, 15*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Task completed! Status: %s\n", resp.Status)
}
