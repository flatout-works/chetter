//go:build ignore
// +build ignore

// opencode_task publishes an OpenCode prompt task to a running runner
// and captures the generated output.
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
		apiKey = os.Getenv("ANTHROPIC_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		apiKey = os.Getenv("GEMINI_API_KEY")
	}
	if apiKey == "" {
		log.Println("WARNING: No LLM API key found in env. Set SYNTHETIC_API_KEY or ANTHROPIC_API_KEY etc.")
	}

	taskID := fmt.Sprintf("opencode-%d", time.Now().Unix())
	req := task.TaskRequest{
		TaskID:     taskID,
		AgentImage: "devfleet/opencode:latest",
		GitURL:     "https://github.com/octocat/Hello-World.git",
		Prompt:     "Add a TODO comment to the top of README.md: <!-- TODO: This was edited by OpenCode inside Devfleet Runner -->",
		TimeoutSec: 180,
		Env: map[string]string{
			"SYNTHETIC_API_KEY": apiKey,
		},
	}

	fmt.Printf("  GitURL: %s\n", req.GitURL)
	fmt.Printf("  Prompt: %s\n", req.Prompt)

	resp, err := testutil.RunTaskAndWait(nc, req, 120*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	if resp.Summary != "" {
		fmt.Printf("\n=== Agent Output ===\n%s\n=== End ===\n", resp.Summary)
	}
}
