package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ReadSessionExport reads the opencode SQLite database and renders a markdown
// transcript of the session.
func (oc *OpenCode) ReadSessionExport(wsDir, sessionID string) (string, error) {
	dbPath := filepath.Join(wsDir, ".local", "share", "opencode", "opencode.db")
	if _, err := os.Stat(dbPath); err != nil {
		return "", fmt.Errorf("opencode db not found: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return "", fmt.Errorf("open opencode db: %w", err)
	}
	defer db.Close()

	// Query session title
	var title string
	_ = db.QueryRow("SELECT title FROM sessions WHERE id = ?", sessionID).Scan(&title)

	// Query messages ordered by creation time
	rows, err := db.Query(
		"SELECT id, role, parts, model, created_at FROM messages WHERE session_id = ? ORDER BY created_at ASC",
		sessionID,
	)
	if err != nil {
		return "", fmt.Errorf("query messages: %w", err)
	}
	defer rows.Close()

	var sections []string
	if title != "" {
		sections = append(sections, fmt.Sprintf("# %s\n", title))
	}

	for rows.Next() {
		var msgID, role, partsJSON, model string
		var createdAt int64
		if err := rows.Scan(&msgID, &role, &partsJSON, &model, &createdAt); err != nil {
			slog.Warn("scan message row failed", "err", err)
			continue
		}
		section := renderMessage(role, model, partsJSON, createdAt)
		if section != "" {
			sections = append(sections, section)
		}
	}

	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("message rows error: %w", err)
	}

	if len(sections) == 0 {
		return "", fmt.Errorf("no messages found for session %s", sessionID)
	}

	return strings.Join(sections, "\n\n---\n\n") + "\n", nil
}

// renderMessage converts a single message's parts into a markdown section.
func renderMessage(role, model, partsJSON string, createdAt int64) string {
	var parts []partWrapper
	if err := json.Unmarshal([]byte(partsJSON), &parts); err != nil {
		return fmt.Sprintf("**%s** (failed to parse parts: %v)\n", strings.Title(role), err)
	}

	header := fmt.Sprintf("## %s", strings.Title(role))
	if model != "" {
		header += fmt.Sprintf(" (%s)", model)
	}
	if createdAt > 0 {
		header += fmt.Sprintf(" — %s", time.UnixMilli(createdAt).UTC().Format(time.RFC3339))
	}

	var body []string
	for _, part := range parts {
		rendered := renderPart(part)
		if rendered != "" {
			body = append(body, rendered)
		}
	}

	if len(body) == 0 {
		return ""
	}

	return header + "\n\n" + strings.Join(body, "\n\n")
}

// renderPart converts one content part to markdown.
func renderPart(part partWrapper) string {
	switch part.Type {
	case "text":
		var tc struct{ Text string `json:"text"` }
		if err := json.Unmarshal(part.Raw, &tc); err == nil {
			return tc.Text
		}
	case "reasoning":
		var rc struct{ Thinking string `json:"thinking"` }
		if err := json.Unmarshal(part.Raw, &rc); err == nil {
			return fmt.Sprintf("<details><summary>Reasoning</summary>\n\n%s\n\n</details>", rc.Thinking)
		}
	case "tool_call":
		var tc struct {
			Name  string `json:"name"`
			Input string `json:"input"`
		}
		if err := json.Unmarshal(part.Raw, &tc); err == nil {
			return fmt.Sprintf("**Tool Call:** `%s`\n\n```json\n%s\n```", tc.Name, tc.Input)
		}
	case "tool_result":
		var tr struct {
			Name    string `json:"name"`
			Content string `json:"content"`
			IsError bool   `json:"is_error"`
		}
		if err := json.Unmarshal(part.Raw, &tr); err == nil {
			status := "OK"
			if tr.IsError {
				status = "Error"
			}
			return fmt.Sprintf("**Tool Result** (%s: %s)\n\n```\n%s\n```", tr.Name, status, tr.Content)
		}
	case "finish":
		var f struct {
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(part.Raw, &f); err == nil {
			return fmt.Sprintf("*Finish: %s*", f.Reason)
		}
	case "image_url":
		var iuc struct{ URL string `json:"url"` }
		if err := json.Unmarshal(part.Raw, &iuc); err == nil {
			return fmt.Sprintf("![Image](%s)", iuc.URL)
		}
	case "binary":
		var bc struct{ Path string `json:"path"` }
		if err := json.Unmarshal(part.Raw, &bc); err == nil {
			return fmt.Sprintf("*[Binary file: %s]*", bc.Path)
		}
	}
	return ""
}

// partWrapper mirrors opencode's discriminated union envelope.
type partWrapper struct {
	Type string          `json:"type"`
	Raw  json.RawMessage `json:"data"`
}
