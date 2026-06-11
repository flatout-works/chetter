//go:build ignore
// +build ignore

// smoke_task publishes a minimal shell-echo task against a running runner
// and waits for its completion status.
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

	taskID := fmt.Sprintf("smoke-%d", time.Now().Unix())
	req := task.TaskRequest{
		TaskID:     taskID,
		AgentImage: "docker.io/library/alpine:latest",
		Command:    []string{"/bin/sh", "-c", "echo hello from kata smoke"},
		TimeoutSec: 30,
	}

	resp, err := testutil.RunTaskAndWait(nc, req, 120*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Result: %+v\n", resp)
}
