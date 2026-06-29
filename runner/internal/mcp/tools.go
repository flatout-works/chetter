package mcp

// ToolDefinitions returns tool definitions for all MCP tools the runner
// exposes to agents. These are pure metadata; handlers are registered
// separately via Server.RegisterTool.
func ToolDefinitions() []ToolDef {
	return []ToolDef{
		{
			Name:        "chetter_create_issue",
			Description: "Create a GitHub issue with a canonical Chetter signature and artifact tracking. Requires a repo-only task scope, not an existing PR or issue scope.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo":   map[string]string{"type": "string", "description": "Repository, e.g. flatout-works/chetter"},
					"title":  map[string]string{"type": "string", "description": "Issue title"},
					"body":   map[string]string{"type": "string", "description": "Issue body without the Chetter footer"},
					"labels": map[string]any{"type": "array", "items": map[string]string{"type": "string"}},
				},
				"required": []string{"repo", "title"},
			},
		},
		{
			Name:        "chetter_issue_comment",
			Description: "Create a GitHub issue or PR comment with a canonical Chetter signature and artifact tracking.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo":         map[string]string{"type": "string", "description": "Repository, e.g. flatout-works/chetter"},
					"issue_number": map[string]string{"type": "integer", "description": "Issue or PR number"},
					"body":         map[string]string{"type": "string", "description": "Comment body without the Chetter footer"},
				},
				"required": []string{"repo", "issue_number", "body"},
			},
		},
		{
			Name:        "chetter_create_pr",
			Description: "Create a GitHub pull request with a canonical Chetter signature and artifact tracking. Requires a repo-only task scope, not an existing PR or issue scope.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo":  map[string]string{"type": "string", "description": "Repository, e.g. flatout-works/chetter"},
					"title": map[string]string{"type": "string", "description": "Pull request title"},
					"body":  map[string]string{"type": "string", "description": "Pull request body without the Chetter footer"},
					"head":  map[string]string{"type": "string", "description": "Head branch or owner:branch"},
					"base":  map[string]string{"type": "string", "description": "Base branch"},
					"draft": map[string]string{"type": "boolean", "description": "Create a draft pull request"},
				},
				"required": []string{"repo", "title", "head", "base"},
			},
		},
		{
			Name:        "chetter_pr_review",
			Description: "Create a GitHub pull request review with a canonical Chetter signature and artifact tracking.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"repo":      map[string]string{"type": "string", "description": "Repository, e.g. flatout-works/chetter"},
					"pr_number": map[string]string{"type": "integer", "description": "Pull request number"},
					"event":     map[string]string{"type": "string", "description": "COMMENT, APPROVE, or REQUEST_CHANGES"},
					"body":      map[string]string{"type": "string", "description": "Review body without the Chetter footer"},
				},
				"required": []string{"repo", "pr_number", "body"},
			},
		},
	}
}
