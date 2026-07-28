package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	piExportHeader            = "# Pi Session Export"
	compactToolResultMaxBytes = 8 * 1024
	compactToolResultMaxLines = 120
	compactToolArgsMaxBytes   = 2 * 1024
)

type piExportMessage struct {
	role string
	body string
}

func compactSessionExport(export string) string {
	if !strings.HasPrefix(strings.TrimSpace(export), piExportHeader) {
		return export
	}
	return compactPiSessionExport(export)
}

func compactPiSessionExport(export string) string {
	messages := parsePiExportMessages(export)
	if len(messages) == 0 {
		return export
	}

	var b strings.Builder
	b.WriteString("# Pi Session Activity\n\n")
	b.WriteString("> Compact view. Tool results are previewed; the full export remains available for download.\n\n")
	pendingTools := make([]string, 0)
	for i, message := range messages {
		role := strings.ToLower(message.role)
		switch role {
		case "assistant":
			body, tools := compactPiAssistantMessage(message.body)
			pendingTools = append(pendingTools, tools...)
			if body == "" {
				continue
			}
			writeCompactMessage(&b, i+1, "Assistant", body)
		case "toolresult", "tool_result", "tool":
			label := "Tool result"
			if len(pendingTools) > 0 {
				label = "Result: " + pendingTools[0]
				pendingTools = pendingTools[1:]
			}
			preview, truncated, originalBytes, originalLines := compactTextPreview(message.body, compactToolResultMaxBytes, compactToolResultMaxLines)
			var body strings.Builder
			if preview != "" {
				body.WriteString(indentMarkdownCode(preview))
				body.WriteString("\n\n")
			}
			if truncated {
				body.WriteString(fmt.Sprintf("> Result truncated in compact view (%d bytes, %d lines). Download the full export for complete content.", originalBytes, originalLines))
			}
			writeCompactMessage(&b, i+1, label, strings.TrimSpace(body.String()))
		default:
			label := message.role
			if role == "user" {
				label = "User"
			}
			writeCompactMessage(&b, i+1, label, message.body)
		}
	}
	return b.String()
}

func parsePiExportMessages(export string) []piExportMessage {
	lines := strings.Split(strings.ReplaceAll(export, "\r\n", "\n"), "\n")
	messages := make([]piExportMessage, 0)
	var current *piExportMessage
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			heading := strings.TrimPrefix(line, "## ")
			if dot := strings.Index(heading, ". "); dot > 0 {
				messages = append(messages, piExportMessage{role: strings.TrimSpace(heading[dot+2:])})
				current = &messages[len(messages)-1]
				continue
			}
		}
		if current != nil {
			if current.body != "" {
				current.body += "\n"
			}
			current.body += line
		}
	}
	for i := range messages {
		messages[i].body = strings.TrimSpace(messages[i].body)
	}
	return messages
}

func compactPiAssistantMessage(body string) (string, []string) {
	raw := strings.TrimSpace(body)
	if strings.HasPrefix(raw, "```json") && strings.HasSuffix(raw, "```") {
		raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(raw, "```json"), "```"))
	}
	var message map[string]any
	if err := json.Unmarshal([]byte(raw), &message); err != nil {
		preview, truncated, originalBytes, originalLines := compactTextPreview(body, compactToolResultMaxBytes, compactToolResultMaxLines)
		if truncated {
			preview += fmt.Sprintf("\n\n> Assistant payload truncated in compact view (%d bytes, %d lines).", originalBytes, originalLines)
		}
		return preview, nil
	}

	content, _ := message["content"].([]any)
	parts := make([]string, 0)
	tools := make([]string, 0)
	for _, rawBlock := range content {
		block, _ := rawBlock.(map[string]any)
		blockType, _ := block["type"].(string)
		switch blockType {
		case "text":
			if text, _ := block["text"].(string); strings.TrimSpace(text) != "" {
				parts = append(parts, strings.TrimSpace(text))
			}
		case "toolCall", "tool_call":
			name, _ := block["name"].(string)
			if name == "" {
				name = "tool"
			}
			tools = append(tools, name)
			parts = append(parts, renderCompactToolCall(name, block["arguments"]))
		}
	}
	return strings.Join(parts, "\n\n"), tools
}

func renderCompactToolCall(name string, arguments any) string {
	var b strings.Builder
	b.WriteString("### Tool: `")
	b.WriteString(name)
	b.WriteString("`\n\n")
	args, _ := arguments.(map[string]any)
	switch name {
	case "bash":
		if command, _ := args["command"].(string); command != "" {
			preview, truncated, originalBytes, _ := compactTextPreview(command, compactToolArgsMaxBytes, compactToolResultMaxLines)
			b.WriteString(indentMarkdownCode(preview))
			if truncated {
				b.WriteString(fmt.Sprintf("\n\n> Command truncated (%d bytes).", originalBytes))
			}
			return b.String()
		}
	case "read":
		path := firstString(args, "filePath", "path")
		if path != "" {
			b.WriteString("`")
			b.WriteString(path)
			b.WriteString("`")
			if offset, ok := args["offset"]; ok {
				b.WriteString(fmt.Sprintf(", offset %v", offset))
			}
			if limit, ok := args["limit"]; ok {
				b.WriteString(fmt.Sprintf(", limit %v", limit))
			}
			return b.String()
		}
	case "edit", "write":
		path := firstString(args, "filePath", "path")
		if path != "" {
			b.WriteString("`")
			b.WriteString(path)
			b.WriteString("`\n\n_Edit content omitted from compact view._")
			return b.String()
		}
	}

	data, err := json.MarshalIndent(arguments, "", "  ")
	if err != nil || string(data) == "null" {
		b.WriteString("_No arguments._")
		return b.String()
	}
	preview, truncated, originalBytes, _ := compactTextPreview(string(data), compactToolArgsMaxBytes, compactToolResultMaxLines)
	b.WriteString(indentMarkdownCode(preview))
	if truncated {
		b.WriteString(fmt.Sprintf("\n\n> Arguments truncated (%d bytes).", originalBytes))
	}
	return b.String()
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, _ := values[key].(string); value != "" {
			return value
		}
	}
	return ""
}

func compactTextPreview(text string, maxBytes, maxLines int) (string, bool, int, int) {
	text = strings.TrimSpace(text)
	originalBytes := len([]byte(text))
	originalLines := 0
	if text != "" {
		originalLines = strings.Count(text, "\n") + 1
	}
	preview := text
	truncated := false
	lines := strings.Split(preview, "\n")
	if len(lines) > maxLines {
		preview = strings.Join(lines[:maxLines], "\n")
		truncated = true
	}
	if len([]byte(preview)) > maxBytes {
		preview = preview[:maxBytes]
		for !utf8.ValidString(preview) {
			preview = preview[:len(preview)-1]
		}
		truncated = true
	}
	return strings.TrimSpace(preview), truncated, originalBytes, originalLines
}

func indentMarkdownCode(text string) string {
	return "    " + strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n    ")
}

func writeCompactMessage(b *strings.Builder, sequence int, label, body string) {
	body = strings.TrimSpace(body)
	if body == "" {
		return
	}
	b.WriteString(fmt.Sprintf("## %d. %s\n\n", sequence, label))
	b.WriteString(body)
	b.WriteString("\n\n")
}
