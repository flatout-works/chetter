//go:build ignore
// +build ignore

// policy_task runs network-policy smoke tests (allowed HTTP, blocked
// domains, blocked metadata IP, blocked DNS) via Kata tasks.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/flatout-works/devfleet/runner/internal/task"
	"github.com/flatout-works/devfleet/runner/test/testutil"
	"github.com/nats-io/nats.go"
)

type policyCase struct {
	name            string
	command         string
	wantSummaryPart string
}

func main() {
	nc := testutil.ConnectNATS()
	defer nc.Close()

	cases := []policyCase{
		{
			name:            "allowed-http",
			command:         "wget -T 15 -qO- http://example.com >/tmp/allowed.out && echo allowed-http-ok",
			wantSummaryPart: "allowed-http-ok",
		},
		{
			name:            "blocked-http-domain",
			command:         "if wget -T 10 -qO- http://pastebin.com >/tmp/blocked.out 2>/tmp/blocked.err; then echo blocked-http-unexpected-success; exit 1; else echo blocked-http-ok; fi",
			wantSummaryPart: "blocked-http-ok",
		},
		{
			name:            "blocked-metadata-ip",
			command:         "if wget -T 5 -qO- http://169.254.169.254 >/tmp/metadata.out 2>/tmp/metadata.err; then echo metadata-unexpected-success; exit 1; else echo metadata-blocked-ok; fi",
			wantSummaryPart: "metadata-blocked-ok",
		},
		{
			name:            "blocked-dns-domain",
			command:         "if nslookup metadata.google.internal >/tmp/dns.out 2>/tmp/dns.err; then echo dns-unexpected-success; exit 1; else echo dns-blocked-ok; fi",
			wantSummaryPart: "dns-blocked-ok",
		},
	}

	for _, tc := range cases {
		if err := runCase(nc, tc); err != nil {
			log.Fatal(err)
		}
	}
	fmt.Println("All policy tests passed.")
	os.Exit(0)
}

func runCase(nc *nats.Conn, tc policyCase) error {
	taskID := fmt.Sprintf("policy-%s-%d", tc.name, time.Now().UnixNano())
	req := task.TaskRequest{
		TaskID:     taskID,
		AgentImage: "docker.io/library/alpine:latest",
		Command:    []string{"/bin/sh", "-c", tc.command},
		TimeoutSec: 45,
	}

	statusSubject := fmt.Sprintf(testutil.DefaultStatusFmt, taskID)
	ch, sub, err := testutil.PublishAndListen(nc, req, testutil.DefaultTasksSubject, statusSubject)
	if err != nil {
		return fmt.Errorf("%s: %w", tc.name, err)
	}
	defer sub.Unsubscribe()

	resp, err := testutil.WaitForTerminal(ch, taskID, 120*time.Second, func(resp task.TaskResponse) {
		data, _ := json.Marshal(resp)
		fmt.Printf("[%s] %s\n", tc.name, string(data))
	})
	if err != nil {
		return fmt.Errorf("%s: %w", tc.name, err)
	}
	if !strings.Contains(resp.Summary, tc.wantSummaryPart) {
		return fmt.Errorf("%s summary %q missing %q", tc.name, resp.Summary, tc.wantSummaryPart)
	}
	return nil
}
