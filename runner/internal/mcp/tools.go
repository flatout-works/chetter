package mcp

// ToolDefinitions returns the full JSON schema (name, description,
// inputSchema) for every MCP tool the runner exposes to agents.
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
			"name":        "workspace_bash",
			"description": "Run a shell command in the workspace. Has no network access beyond the proxy.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":     map[string]string{"type": "string"},
					"timeout_sec": map[string]string{"type": "integer", "default": "60"},
				},
				"required": []string{"command"},
			},
		},
		{
			"name":        "git_status",
			"description": "Get git status of the workspace as structured JSON.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name":        "git_pull",
			"description": "Pull latest changes using runner credentials.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"remote": map[string]string{"type": "string", "default": "origin"},
					"branch": map[string]string{"type": "string"},
				},
			},
		},
		{
			"name":        "git_push",
			"description": "Push workspace changes using runner credentials.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"remote": map[string]string{"type": "string", "default": "origin"},
					"branch": map[string]string{"type": "string"},
				},
			},
		},
		{
			"name":        "git_commit",
			"description": "Commit changes in the workspace.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]string{"type": "string"},
					"all":     map[string]string{"type": "boolean", "default": "false"},
				},
				"required": []string{"message"},
			},
		},
		{
			"name":        "nats_publish",
			"description": "Publish a message to NATS via the runner.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"subject": map[string]string{"type": "string"},
					"payload": map[string]string{"type": "string"},
					"headers": map[string]string{"type": "object"},
				},
				"required": []string{"subject", "payload"},
			},
		},
		{
			"name":        "nats_request",
			"description": "Send a NATS request-response via the runner.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"subject":     map[string]string{"type": "string"},
					"payload":     map[string]string{"type": "string"},
					"timeout_sec": map[string]string{"type": "integer", "default": "30"},
				},
				"required": []string{"subject", "payload"},
			},
		},
		{
			"name":        "fetch_url",
			"description": "Fetch a URL via the runner's network egress with filtering.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"url":     map[string]string{"type": "string"},
					"method":  map[string]string{"type": "string", "enum": "GET,POST,PUT,DELETE", "default": "GET"},
					"headers": map[string]string{"type": "object"},
					"body":    map[string]string{"type": "string"},
				},
				"required": []string{"url"},
			},
		},
		{
			"name":        "deploy_build",
			"description": "Build a Docker image from the workspace. Each build is tagged with a unique version (git short SHA or timestamp) AND as :latest. The version tag is returned in the response — save it to roll back later. If no Dockerfile is found, auto-detects Dockerfile, docker/Dockerfile, or .deploy/Dockerfile.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"dockerfile":  map[string]string{"type": "string", "description": "Path to Dockerfile relative to workspace. Auto-detected if empty.", "default": "Dockerfile"},
					"context":     map[string]string{"type": "string", "description": "Build context directory relative to workspace", "default": "."},
					"tag":         map[string]string{"type": "string", "description": "Explicit image tag. If empty, auto-generated as <base>:<git-short-sha>"},
					"build_args":  map[string]string{"type": "array", "description": "Docker build args as KEY=VALUE strings"},
					"timeout_sec": map[string]string{"type": "integer", "default": "300"},
				},
			},
		},
		{
			"name":        "deploy_push",
			"description": "Push a Docker image to the configured registry. Pushes both the versioned tag and :latest. Required for preview deploys (preview provider) where images are pulled by Arcane on Wowbagger. Not needed for local deploys.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"image":       map[string]string{"type": "string", "description": "Versioned image tag to push (e.g. registry/devfleet-abc:def4567). Defaults to :latest if empty."},
					"timeout_sec": map[string]string{"type": "integer", "default": "300"},
				},
			},
		},
		{
			"name":        "deploy_run",
			"description": "Run a container from a built image on the runner host. Stops any existing container with the same name first (redeploy). For local provider: accessible at http://localhost:<port>. For preview provider: accessible at https://<task>.devfleet.works. Returns the access URL in the response.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"image":       map[string]string{"type": "string", "description": "Image to run. Use a versioned tag (from deploy_build response) to pin, or :latest for the most recent build."},
					"name":        map[string]string{"type": "string", "description": "Container name (auto-generated from task ID if empty)"},
					"port":        map[string]string{"type": "string", "description": "Port to expose on the host", "default": "8080"},
					"env":         map[string]string{"type": "object", "description": "Environment variables for the container (e.g. DATABASE_URL, API_KEY)"},
					"detach":      map[string]string{"type": "boolean", "default": "true", "description": "Run in background. Set false to block and see output."},
					"timeout_sec": map[string]string{"type": "integer", "default": "120"},
				},
			},
		},
		{
			"name":        "deploy_status",
			"description": "Check whether a deployed container is running, and on which ports. Returns container state, status, and port mappings.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]string{"type": "string", "description": "Container name (auto-generated from task ID if empty)"},
				},
			},
		},
		{
			"name":        "deploy_stop",
			"description": "Stop a running deployment container gracefully. Use before redeploying or when cleaning up.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":    map[string]string{"type": "string", "description": "Container name (auto-generated from task ID if empty)"},
					"timeout": map[string]string{"type": "string", "description": "Seconds to wait for graceful shutdown before killing", "default": "10"},
				},
			},
		},
		{
			"name":        "deploy_logs",
			"description": "Get stdout/stderr logs from a deployed container. Useful for debugging startup failures or runtime errors.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]string{"type": "string", "description": "Container name (auto-generated from task ID if empty)"},
					"tail": map[string]string{"type": "string", "description": "Number of recent lines to return", "default": "100"},
				},
			},
		},
		{
			"name":        "deploy_list",
			"description": "List all deployment containers on this runner. Shows container name, image, status, and exposed ports.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"all":    map[string]string{"type": "boolean", "description": "Include stopped containers", "default": "false"},
					"filter": map[string]string{"type": "string", "description": "Docker filter expression (e.g. 'name=devfleet-')"},
				},
			},
		},
		{
			"name":        "deploy_versions",
			"description": "List all built image versions for this project. Each build creates a versioned tag (git SHA or timestamp) plus :latest. Use this to find a previous version to roll back to.",
			"inputSchema": map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			"name":        "deploy_rollback",
			"description": "Roll back to a previous image version. Stops the current container and starts a new one from the specified image tag. Use deploy_versions first to find available tags.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"image": map[string]string{"type": "string", "description": "Exact image tag to roll back to (required, e.g. devfleet-abc:def4567)"},
					"name":  map[string]string{"type": "string", "description": "Container name (auto-generated from task ID if empty)"},
					"port":  map[string]string{"type": "string", "description": "Port to expose", "default": "8080"},
					"env":   map[string]string{"type": "object", "description": "Environment variables for the container (e.g. DATABASE_URL, API_KEY)"},
				},
				"required": []string{"image"},
			},
		},

	}
}
