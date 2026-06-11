package testutil

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/flatout-works/devfleet/runner/internal/task"
	"github.com/nats-io/nats.go"
)

const (
	DefaultTasksSubject  = "devfleet.runner.tasks"
	DefaultStatusFmt     = "devfleet.tasks.%s.status"
)

func ConnectNATS() *nats.Conn {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}
	nc, err := nats.Connect(natsURL)
	if err != nil {
		log.Fatalf("nats connect: %v", err)
	}
	return nc
}

func PublishAndListen(nc *nats.Conn, req task.TaskRequest, tasksSubject, statusSubject string) (<-chan task.TaskResponse, *nats.Subscription, error) {
	resultCh := make(chan task.TaskResponse, 8)
	sub, err := nc.Subscribe(statusSubject, func(msg *nats.Msg) {
		var resp task.TaskResponse
		if err := json.Unmarshal(msg.Data, &resp); err != nil {
			log.Printf("decode status for %s: %v", req.TaskID, err)
			return
		}
		resultCh <- resp
	})
	if err != nil {
		return nil, nil, fmt.Errorf("subscribe: %w", err)
	}
	if err := nc.Flush(); err != nil {
		sub.Unsubscribe()
		return nil, nil, fmt.Errorf("subscribe flush: %w", err)
	}

	data, err := json.Marshal(req)
	if err != nil {
		sub.Unsubscribe()
		return nil, nil, fmt.Errorf("marshal: %w", err)
	}
	if err := nc.Publish(tasksSubject, data); err != nil {
		sub.Unsubscribe()
		return nil, nil, fmt.Errorf("publish: %w", err)
	}
	if err := nc.Flush(); err != nil {
		sub.Unsubscribe()
		return nil, nil, fmt.Errorf("publish flush: %w", err)
	}

	fmt.Printf("Published task %s\n", req.TaskID)
	return resultCh, sub, nil
}

func WaitForTerminal(ch <-chan task.TaskResponse, taskID string, timeout time.Duration, onStatus func(resp task.TaskResponse)) (*task.TaskResponse, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return nil, fmt.Errorf("timeout waiting for task %s", taskID)
		case resp := <-ch:
			fmt.Printf("[%s] Status: %s\n", time.Now().Format("15:04:05"), resp.Status)
			if resp.Error != "" {
				fmt.Printf("  Error: %s\n", resp.Error)
			}
			if onStatus != nil {
				onStatus(resp)
			}
			switch resp.Status {
			case "done":
				return &resp, nil
			case "error", "cancelled":
				return &resp, fmt.Errorf("task %s: %s - %s", resp.Status, resp.Error, resp.Summary)
			default:
				data, _ := json.Marshal(resp)
				fmt.Printf("  Data: %s\n", string(data))
			}
		}
	}
}

func RunTaskAndWait(nc *nats.Conn, req task.TaskRequest, timeout time.Duration) (*task.TaskResponse, error) {
	statusSubject := fmt.Sprintf(DefaultStatusFmt, req.TaskID)
	ch, sub, err := PublishAndListen(nc, req, DefaultTasksSubject, statusSubject)
	if err != nil {
		return nil, err
	}
	defer sub.Unsubscribe()
	return WaitForTerminal(ch, req.TaskID, timeout, nil)
}
