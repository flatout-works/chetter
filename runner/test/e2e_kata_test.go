//go:build ignore
// +build ignore

// e2e_kata_test publishes a task in either "kata" or "local" mode and
// reports the final status.
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/flatout-works/devfleet/runner/internal/task"
	"github.com/flatout-works/devfleet/runner/test/testutil"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: go run e2e_kata_test.go <kata|local>")
		os.Exit(1)
	}

	mode := os.Args[1]

	nc := testutil.ConnectNATS()
	defer nc.Close()

	taskID := fmt.Sprintf("e2e-%s-%d", mode, time.Now().Unix())
	req := task.TaskRequest{
		TaskID:     taskID,
		AgentImage: "docker.io/library/alpine:latest",
		Command:    []string{"/bin/sh", "-c", "echo hello from kata e2e"},
		TimeoutSec: 120,
	}

	fmt.Printf("Publishing task %s (mode=%s)\n", taskID, mode)
	resp, err := testutil.RunTaskAndWait(nc, req, 30*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Final: %s\n", resp.Status)
}
