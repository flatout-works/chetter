//go:build ignore
// +build ignore

// local_task publishes a minimal task to a running runner and waits for
// it to reach a terminal status (used for local-mode smoke testing).
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

	taskID := fmt.Sprintf("local-%d", time.Now().Unix())
	req := task.TaskRequest{
		TaskID:     taskID,
		AgentImage: "docker.io/library/alpine:latest",
		TimeoutSec: 30,
	}

	resp, err := testutil.RunTaskAndWait(nc, req, 15*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Final: %s\n", resp.Status)
}
