//go:build ignore
// +build ignore

// opencode_kata_task.go sends a task to an ALREADY-RUNNING runner in Kata mode.
// Start the runner first: sudo ./runner -config runner.yaml
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
	nc := testutil.ConnectNATS()
	defer nc.Close()

	apiKey := os.Getenv("SYNTHETIC_API_KEY")
	if apiKey == "" {
		log.Fatal("Set SYNTHETIC_API_KEY env var")
	}

	taskID := fmt.Sprintf("kata-poc-%d", time.Now().Unix())
	req := task.TaskRequest{
		TaskID:     taskID,
		AgentImage: "docker.io/devfleet/opencode:latest",
		GitURL:     "https://github.com/octocat/Hello-World.git",
		Prompt:     "List the files in README.md and summarize what this project does in one sentence.",
		TimeoutSec: 300,
		Env: map[string]string{
			"SYNTHETIC_API_KEY": apiKey,
		},
	}

	resp, err := testutil.RunTaskAndWait(nc, req, 300*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	if resp.Summary != "" {
		fmt.Printf("Summary:\n%s\n", resp.Summary)
	}
	if resp.Error != "" {
		fmt.Printf("Error: %s\n", resp.Error)
	}
}
