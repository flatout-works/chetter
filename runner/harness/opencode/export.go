package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

func (oc *OpenCode) ReadSessionExport(wsDir, sessionID string) (string, error) {
	dbPath := filepath.Join(wsDir, ".local", "share", "opencode", "opencode.db")
	if _, err := os.Stat(dbPath); err != nil {
		fallback := filepath.Join(wsDir, ".opencode", "opencode.db")
		if _, err2 := os.Stat(fallback); err2 != nil {
			fallback2 := filepath.Join(wsDir, ".opencode-db", "opencode.db")
			if _, err3 := os.Stat(fallback2); err3 != nil {
				return "", fmt.Errorf("opencode db not found at %s, %s, or %s", dbPath, fallback, fallback2)
			}
			dbPath = fallback2
		} else {
			dbPath = fallback
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return "", fmt.Errorf("open opencode db: %w", err)
	}
	defer db.Close()

	if _, err := db.Exec("PRAGMA busy_timeout = 15000"); err != nil {
		slog.Warn("sqlite busy_timeout pragma failed", "error", err)
	}

	var title, agent, modelID string
	var cost float64
	var tokensIn, tokensOut int64
	_ = db.QueryRow("SELECT title, COALESCE(agent,''), COALESCE(model,''), COALESCE(cost,0), COALESCE(tokens_input,0), COALESCE(tokens_output,0) FROM session WHERE id = ?", sessionID).Scan(&title, &agent, &modelID, &cost, &tokensIn, &tokensOut)
	if title == "" {
		_ = db.QueryRow("SELECT title, COALESCE(agent,''), COALESCE(model,''), COALESCE(cost,0), COALESCE(tokens_input,0), COALESCE(tokens_output,0) FROM sessions WHERE id = ?", sessionID).Scan(&title, &agent, &modelID, &cost, &tokensIn, &tokensOut)
	}

	rows, err := db.Query(
		"SELECT id FROM message WHERE session_id = ? ORDER BY id ASC",
		sessionID,
	)
	if err != nil {
		rows, err = db.Query(
			"SELECT id FROM messages WHERE session_id = ? ORDER BY id ASC",
			sessionID,
		)
	}
	if err != nil {
		return "", fmt.Errorf("query messages: %w", err)
	}

	var msgIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			slog.Warn("scan message id failed", "err", err)
			continue
		}
		msgIDs = append(msgIDs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("message ids error: %w", err)
	}

	var sections []string

	if title != "" {
		header := fmt.Sprintf("# %s", title)
		if agent != "" {
			header += fmt.Sprintf(" (%s)", agent)
		}
		meta := ""
		if cost > 0 || tokensIn > 0 {
			meta += fmt.Sprintf("Tokens: %d in / %d out", tokensIn, tokensOut)
			if cost > 0 {
				meta += fmt.Sprintf(" | Cost: $%.4f", cost)
			}
		}
		if meta != "" {
			header += "\n\n" + meta
		}
		sections = append(sections, header)
	}

	for i, msgID := range msgIDs {
		role := "user"
		if i == 0 {
			// first message is user input
		} else {
			role = "assistant"
		}

		partRows, err := db.Query("SELECT data FROM part WHERE message_id = ? ORDER BY id ASC", msgID)
		if err != nil {
			partRows, err = db.Query("SELECT data FROM parts WHERE message_id = ? ORDER BY id ASC", msgID)
		}
		if err != nil {
			slog.Warn("query parts failed", "message_id", msgID, "err", err)
			continue
		}
		var parts []string
		for partRows.Next() {
			var partData string
			if err := partRows.Scan(&partData); err != nil {
				slog.Warn("scan part failed", "err", err)
				continue
			}
			parts = append(parts, partData)
		}
		partRows.Close()

		body := renderMessageParts(role, parts)
		if body != "" {
			sections = append(sections, body)
		}
	}
	if len(sections) == 0 {
		return "", fmt.Errorf("no messages found for session %s", sessionID)
	}

	return strings.Join(sections, "\n\n") + "\n", nil
}

func renderMessageParts(role string, parts []string) string {
	var lines []string
	for _, raw := range parts {
		var p map[string]any
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			continue
		}
		typ, _ := p["type"].(string)
		switch typ {
		case "text":
			if t, _ := p["text"].(string); t != "" {
				lines = append(lines, escapeExportRoleHeadings(t))
			}
		case "reasoning":
			if t, _ := p["text"].(string); t != "" {
				lines = append(lines, fmt.Sprintf("<details><summary>Reasoning</summary>\n\n%s\n\n</details>", escapeExportRoleHeadings(t)))
			}
		case "step-finish":
			if tokens, ok := p["tokens"].(map[string]any); ok {
				parts := []string{}
				if v, _ := tokens["total"].(float64); v > 0 {
					parts = append(parts, fmt.Sprintf("%.0f total", v))
				}
				if v, _ := tokens["input"].(float64); v > 0 {
					parts = append(parts, fmt.Sprintf("%.0f in", v))
				}
				if v, _ := tokens["output"].(float64); v > 0 {
					parts = append(parts, fmt.Sprintf("%.0f out", v))
				}
				if len(parts) > 0 {
					lines = append(lines, "*Tokens: "+strings.Join(parts, ", ")+"*")
				}
			}
		case "tool-use", "tool_call":
			name, _ := p["name"].(string)
			input, _ := p["input"].(string)
			if name != "" && input != "" {
				var prettyJSON string
				var parsed any
				if json.Unmarshal([]byte(input), &parsed) == nil {
					b, _ := json.MarshalIndent(parsed, "", "  ")
					prettyJSON = string(b)
				} else {
					prettyJSON = input
				}
				lines = append(lines, fmt.Sprintf("**Tool Call:** `%s`\n\n```json\n%s\n```", name, prettyJSON))
			}
		case "tool-result", "tool_result":
			content, _ := p["content"].(string)
			name, _ := p["name"].(string)
			isErr, _ := p["is_error"].(bool)
			status := "OK"
			if isErr {
				status = "Error"
			}
			if name != "" {
				lines = append(lines, fmt.Sprintf("**Tool Result** (%s: %s)\n\n```\n%s\n```", name, status, escapeExportRoleHeadings(content)))
			} else if content != "" {
				lines = append(lines, fmt.Sprintf("**Tool Result**\n\n```\n%s\n```", escapeExportRoleHeadings(content)))
			}
		case "step-start":
		}
	}

	if len(lines) == 0 {
		return ""
	}

	header := fmt.Sprintf("## %s", titleCase(role))
	return header + "\n\n" + strings.Join(lines, "\n\n")
}

func escapeExportRoleHeadings(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i, line := range lines {
		switch strings.TrimSpace(line) {
		case "## User", "## Assistant", "## Tool", "## System", "## Developer":
			lines[i] = `\` + line
		}
	}
	return strings.Join(lines, "\n")
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r)
}
