//go:build ignore
// +build ignore

// opencode_smoke starts a local-mode runner, sends an OpenCode++ prompt
// task, and waits for the result.
package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/flatout-works/devfleet/runner/internal/task"
	"github.com/flatout-works/devfleet/runner/test/testutil"
)

func main() {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}

	// Start runner as a child process
	runnerCmd := exec.Command("./runner", "-config", "test.runner.yaml")
	runnerCmd.Env = append(os.Environ(), "RUNNER_LOCAL=true")
	runnerCmd.Dir = mustGetwd()

	runnerOut, err := runnerCmd.StderrPipe()
	if err != nil {
		log.Fatalf("runner pipe: %v", err)
	}
	if err := runnerCmd.Start(); err != nil {
		log.Fatalf("runner start: %v", err)
	}
	defer func() {
		runnerCmd.Process.Kill()
		runnerCmd.Wait()
	}()

	// Wait for "listening" in runner output
	scanner := bufio.NewScanner(runnerOut)
	ready := make(chan struct{})
	go func() {
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Printf("[runner] %s\n", line)
			if strings.Contains(line, "listening on devfleet.test.tasks") {
				close(ready)
				break
			}
		}
		for scanner.Scan() {
			fmt.Printf("[runner] %s\n", scanner.Text())
		}
	}()

	select {
	case <-ready:
		fmt.Println("Runner is ready.")
	case <-time.After(15 * time.Second):
		log.Fatalf("Runner did not become ready in 15s")
	}

	// Connect to NATS
	nc := testutil.ConnectNATS()
	defer nc.Close()

	// Read API key
	apiKey := os.Getenv("SYNTHETIC_API_KEY")
	if apiKey == "" {
		log.Println("WARNING: No SYNTHETIC_API_KEY set. The LLM call may fail.")
	}

	taskID := fmt.Sprintf("poc-%d", time.Now().Unix())
	req := task.TaskRequest{
		TaskID:     taskID,
		AgentImage: "devfleet/opencode:latest",
		GitURL:     "https://github.com/octocat/Hello-World.git",
		Prompt:     "Add a TODO comment to the TOP of the README file that says '# TODO: This was edited by OpenCode via Devfleet Runner'. Then use git to commit with message 'Add TODO via OpenCode'.",
		TimeoutSec: 300,
		Env: map[string]string{
			"SYNTHETIC_API_KEY": apiKey,
		},
	}

	statusSubject := fmt.Sprintf("devfleet.test.results.%s.status", taskID)
	ch, sub, err := testutil.PublishAndListen(nc, req, "devfleet.test.tasks", statusSubject)
	if err != nil {
		log.Fatal(err)
	}
	defer sub.Unsubscribe()

	resp, err := testutil.WaitForTerminal(ch, taskID, 300*time.Second, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("SUCCESS: task completed")
	if resp.Summary != "" {
		fmt.Println("=== Full Output ===")
		fmt.Println(resp.Summary)
	}
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	return wd
}
