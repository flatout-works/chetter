package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func readSessionExport(wsDir, sessionID string) (string, error) {
	nativeID := sessionID
	mappingPath := filepath.Join(wsDir, ".claude", "chetter-sessions", sessionID+".json")
	if data, err := os.ReadFile(mappingPath); err == nil {
		var mapping struct {
			NativeSessionID string `json:"native_session_id"`
		}
		if err := json.Unmarshal(data, &mapping); err != nil {
			return "", fmt.Errorf("decode Claude session mapping: %w", err)
		}
		if mapping.NativeSessionID == "" {
			return "", fmt.Errorf("claude session mapping has no native session ID")
		}
		nativeID = mapping.NativeSessionID
	}

	transcript, err := findTranscript(filepath.Join(wsDir, ".claude", "projects"), nativeID)
	if err != nil {
		return "", err
	}
	f, err := os.Open(transcript)
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

func findTranscript(projectsDir, sessionID string) (string, error) {
	if sessionID == "" || filepath.Base(sessionID) != sessionID {
		return "", fmt.Errorf("invalid Claude session ID")
	}
	want := sessionID + ".jsonl"
	var exact string
	var transcripts []string
	err := filepath.WalkDir(projectsDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			return nil
		}
		transcripts = append(transcripts, path)
		if entry.Name() == want {
			if exact != "" {
				return fmt.Errorf("multiple transcripts found for Claude session %s", sessionID)
			}
			exact = path
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("find Claude session transcript: %w", err)
	}
	if exact != "" {
		return exact, nil
	}
	// Older workspaces predate durable proxy/native mappings. A sole
	// transcript is unambiguous; multiple transcripts must never be guessed.
	if len(transcripts) == 1 {
		return transcripts[0], nil
	}
	return "", fmt.Errorf("no unique transcript found for Claude session %s", sessionID)
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
