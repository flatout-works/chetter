package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func readSessionExport(wsDir, sessionID string) (string, error) {
	entries, err := os.ReadDir(wsDir + "/.claude/projects")
	if err != nil {
		return "", fmt.Errorf("read projects dir: %w", err)
	}

	var latestDir string
	for _, entry := range entries {
		if entry.IsDir() {
			latestDir = entry.Name()
		}
	}

	if latestDir == "" {
		return "", fmt.Errorf("no Claude project directories found")
	}

	projectPath := wsDir + "/.claude/projects/" + latestDir
	return renderSessionFromDir(projectPath)
}

func renderSessionFromDir(dir string) (string, error) {
	subEntries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read project dir: %w", err)
	}

	latestFile := latestJSONLFile(dir, subEntries)
	if latestFile == "" {
		for _, sub := range subEntries {
			if !sub.IsDir() {
				continue
			}
			entries, err := os.ReadDir(dir + "/" + sub.Name())
			if err != nil {
				continue
			}
			if candidate := latestJSONLFile(dir+"/"+sub.Name(), entries); candidate != "" {
				latestFile = candidate
				break
			}
		}
	}

	if latestFile == "" {
		return "", fmt.Errorf("no session JSONL files in %s", dir)
	}

	f, err := os.Open(latestFile)
	if err != nil {
		return "", err
	}
	defer f.Close()

	var sb strings.Builder
	sb.WriteString("# Claude Session\n\n")

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}

		typ, _ := ev["type"].(string)
		switch typ {
		case "assistant":
			content, _ := ev["message"].(map[string]any)
			if text := claudeMessageText(content); text != "" {
				sb.WriteString(text)
				sb.WriteString("\n\n")
			}
		case "user":
			msg, _ := ev["message"].(map[string]any)
			if text := claudeMessageText(msg); text != "" {
				fmt.Fprintf(&sb, "> %s\n\n", text)
			}
		}
	}

	return sb.String(), scanner.Err()
}

func claudeMessageText(message map[string]any) string {
	if message == nil {
		return ""
	}
	if text, _ := message["text"].(string); text != "" {
		return text
	}
	switch content := message["content"].(type) {
	case string:
		return content
	case []any:
		var text strings.Builder
		for _, item := range content {
			block, _ := item.(map[string]any)
			if block == nil {
				continue
			}
			value, _ := block["text"].(string)
			if value == "" {
				continue
			}
			text.WriteString(value)
		}
		return text.String()
	}
	return ""
}

func latestJSONLFile(dir string, entries []os.DirEntry) string {
	var latest string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			latest = dir + "/" + entry.Name()
		}
	}
	return latest
}
