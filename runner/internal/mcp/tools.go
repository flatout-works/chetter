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
	}
}
