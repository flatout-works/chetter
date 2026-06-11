// smoke_test publishes a basic Kata task and prints the result.
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

	resp, err := testutil.RunTaskAndWait(nc, req, 10*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Result: %+v\n", resp)
}
