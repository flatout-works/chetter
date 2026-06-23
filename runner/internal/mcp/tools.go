package mcp

// ToolDefinitions returns the full JSON schema (name, description,
// inputSchema) for every MCP tool the runner exposes to agents.
//
// Only file I/O tools that operate within the workspace directory are
// exposed. Tools that execute commands on the runner host (workspace_bash,
// git_*, deploy_*, fetch_url) are intentionally excluded to prevent sandbox
// escape — the agent already has shell access inside its container via the
// harness.
func ToolDefinitions() []map[string]any {
	return []map[string]any{
		{
			"name":        "workspace_read_file",
			"description": "Read a file from the workspace.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]string{"type": "string", "description": "Path relative to /workspace"},
				},
				"required": []string{"path"},
			},
		},
		{
			"name":        "workspace_write_file",
			"description": "Write or overwrite a file in the workspace.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":    map[string]string{"type": "string"},
					"content": map[string]string{"type": "string"},
				},
				"required": []string{"path", "content"},
			},
		},
		{
			"name":        "workspace_list_directory",
			"description": "List files and directories in the workspace.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]string{"type": "string", "description": "Directory path relative to /workspace", "default": "."},
				},
			},
		},
		{
			"name":        "chetter_create_issue",
			"description": "Create a GitHub issue with a canonical Chetter signature and artifact tracking.",
			"inputSchema": map[string]any{
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
			"name":        "chetter_issue_comment",
			"description": "Create a GitHub issue or PR comment with a canonical Chetter signature and artifact tracking.",
			"inputSchema": map[string]any{
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
			"name":        "chetter_create_pr",
			"description": "Create a GitHub pull request with a canonical Chetter signature and artifact tracking.",
			"inputSchema": map[string]any{
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
			"name":        "chetter_pr_review",
			"description": "Create a GitHub pull request review with a canonical Chetter signature and artifact tracking.",
			"inputSchema": map[string]any{
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
