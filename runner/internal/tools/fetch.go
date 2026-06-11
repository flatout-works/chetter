package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Fetch handles fetch_url tool calls.
func Fetch(ctx context.Context, args map[string]any) (any, error) {
	url, err := requireString(args, "url")
	if err != nil {
		return nil, err
	}
	method := getOptString(args, "method", "GET")

	var body io.Reader
	if b, ok := args["body"].(string); ok && b != "" {
		body = strings.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if h, ok := args["headers"].(map[string]any); ok {
		for k, v := range h {
			if s, ok := v.(string); ok {
				req.Header.Set(k, s)
			}
		}
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		return string(data), fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return string(data), nil
}
