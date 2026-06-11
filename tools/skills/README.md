# Skills System

This directory contains OpenCode skills used for agentic code generation. Skills are loaded by OpenCode at runtime and provide specialized instructions, patterns, and references to guide the LLM.

## What is an OpenCode Skill?

An OpenCode skill is a markdown package (usually `SKILL.md` + optional `references/` and `scripts/`) that extends the agent with domain-specific knowledge. OpenCode discovers skills by scanning:

1. **Global scope:** `~/.agents/skills/**/SKILL.md`
2. **Project scope:** `/workspace/.agents/skills/**/SKILL.md` (relative to `--dir`)
3. **Custom paths/URLs:** Configured in `config.json` under `skills.paths` and `skills.urls`

Skills have YAML frontmatter with `name`, `description`, and optional metadata. The description is what triggers skill loading — it must be specific enough for the routing system to match.

## Skills for Backend Development

| Skill | Purpose | Triggers |
|-------|---------|----------|
| `flatout-backend` | **Orchestrator.** Defines the full backend stack workflow (ConnectRPC, sqlc, goose, slog, TiDB). Delegates to specialist skills for deep domain work. | "Go backend", "ConnectRPC", "sqlc", "goose", "protobuf service" |
| `golang-pro` | Deep Go expertise: concurrency, generics, interfaces, pprof, benchmarks, project structure, testing. | "goroutines", "channels", "gRPC", "microservices", "Go generics" |
| `tidb-sql` | TiDB-specific SQL: DDL/DML, vector indexes, full-text search, transaction modes, diagnostics. | "TiDB", "MySQL compatibility", "VECTOR", migration review |
| `sqlc` | sqlc configuration, query annotations (`:one`/`:many`/`:exec`), multi-engine codegen. | "sqlc", "generated query", `sqlc.yaml` |
| `protobuf` | Protobuf schema design, field rules, well-known types, buf linting. | "proto", "protobuf", ".proto file" |

## How Skills Are Made Available in the Runner

The runner spawns OpenCode inside Docker/Kata containers. To make skills available there:

### 1. Host-Level Installation (Required)

Skills must be installed on the **host** where the runner process runs. The runner copies them into each workspace before starting OpenCode:

```bash
# Install a skill from the skills.sh registry
npx skills add vercel-labs/agent-skills@go-best-practices -g -y

# Or manually copy into the host agents directory
mkdir -p ~/.agents/skills/flatout-backend
cp tools/skills/flatout-backend/SKILL.md ~/.agents/skills/flatout-backend/
cp -r tools/skills/flatout-backend/references ~/.agents/skills/flatout-backend/
```

### 2. Runner Copies Skills Into Workspace

When a `TaskRequest` arrives, the runner:

1. Creates a fresh workspace directory (`/var/lib/runner/<task-id>/`)
2. Copies `~/.agents/skills/*` into `<workspace>/.agents/skills/`
3. Mounts the workspace into the container at `/workspace`
4. OpenCode discovers the skills at `/workspace/.agents/skills/**/SKILL.md`

This is implemented in `runner/internal/controller/runner.go:copySkillsToWorkspace()`.

### 3. Container Image

The harness image contains the OpenCode binary + MCP bridge but **no skills**. The runner supplies skills at runtime via bind mount.

For stack-specific images (under `tools/stacks/`), the same mechanism applies — skills come from the host via the workspace mount, not baked into the image. This keeps images small and allows updating skills without rebuilding the container.

## Creating a New Skill

1. **Create the directory:**
   ```bash
   mkdir -p ~/.agents/skills/<skill-name>/references
   ```

2. **Write `SKILL.md`:**
   ```markdown
   ---
   name: my-skill
   description: One-sentence trigger description. Use when X, Y, or Z.
   ---
   
   # My Skill
   
   Instructions, patterns, constraints...
   
   ## References
   
   - `references/topic.md` — Deep dive on specific topic
   ```

3. **Add references** (optional but recommended for large topics):
   ```bash
   # References are loaded on-demand when the skill is invoked for a
   # specific sub-topic. Keep SKILL.md focused; offload details to refs.
   ```

4. **Copy to repo if it's project-specific:**
   ```bash
   mkdir -p tools/skills/<skill-name>
   cp -r ~/.agents/skills/<skill-name>/* tools/skills/<skill-name>/
   ```

5. **Test:** Trigger a runner task and verify in logs that the skill is discovered:
   ```
   copied skills ... count=5
   ```
   And in OpenCode's output:
   ```
   <available_skills> ... <name>my-skill</name> ...
   ```

## Skill Design Guidelines

- **Use the `metadata` block** in frontmatter: `triggers`, `role: specialist|architect|generalist`, `scope: implementation|review|design`, `output-format: code|markdown|json`
- **Delegate when possible.** The `flatout-backend` skill is an orchestrator — it describes the workflow and points to `golang-pro`, `tidb-sql`, `sqlc`, and `protobuf` for deep domain details. This avoids duplicating knowledge.
- **Include working code examples.** Go code should compile (or be close to it). SQL should be valid for TiDB.
- **Reference verification commands.** Every skill should tell the agent what to run to verify its output: `go build ./...`, `go vet ./...`, `make generate`, etc.
- **Keep SKILL.md < 300 lines.** Offload long references to `references/*.md`.

## Skill Delivery Pipeline

```
Developer workstation          Runner host                    Kata/Docker container
       │                            │                                    │
       │  Edit SKILL.md             │                                    │
       │─────────────►              │                                    │
       │                            │                                    │
       │  git push                  │                                    │
       │────────────────────────►   │                                    │
       │                            │                                    │
       │                            │  (or via per-stack Dockerfile)
       │                            │                                    │
       │                            │  Task arrives via NATS              │
       │                            │─────────────► copySkillsToWorkspace()
       │                            │               ~/.agents/skills ──► /workspace/.agents/skills/
       │                            │                                    │
       │                            │  start container                   │
       │                            │─────────────────────────────────────►
       │                            │                                    │
       │                            │                                    │  OpenCode scans /workspace/.agents/skills/**/SKILL.md
       │                            │                                    │  Skill loaded into prompt context
```

## Maintenance Checklist

- [ ] Keep host `~/.agents/skills/` in sync with `tools/skills/` in the repo
- [ ] Update `flatout-backend` when the server stack changes (new deps, new tools)
- [ ] Update the per-stack Dockerfile when tool versions change in the server
- [ ] Verify the per-stack Dockerfile still builds correctly
- [ ] Test skill discovery by checking runner logs for `copied skills ... count=N`

## Related Files

| File | Purpose |
|------|---------|
| `runner/internal/controller/runner.go:copySkillsToWorkspace()` | Copies skills at task start |
 
