# Skills Deployment Guide

This document gives exact commands for deploying the `flatout-backend` skill and enabling it in your runner. Each step shows **where** to run it and what output to expect.

## Prerequisites

You need two environments:

| Environment | What it is | Used in steps |
|---|---|---|
| **Workstation** | Your dev machine (where `flatout` repo lives) | Step 1 (git push) |
| **Runner Host** | Server running the runner process + Docker/containerd | Steps 2–5 |

The runner host must have:
- Docker or Podman (`docker ps` works)
- Go 1.26+ installed (to build mcp-bridge)
- The `opencode` binary at `~/.opencode/bin/opencode` (or available to copy)
- Access to the `~/.agents/skills/` directory that OpenCode uses

---

## Step 1: Sync the skill to the runner host

### Option A — Git-based (recommended)

On **your workstation**, commit and push:

```bash
cd /path/to/flatout/repo

git add tools/skills/flatout-backend/
git add tools/skills/README.md
git add runner/Makefile
git add runner/internal/controller/runner.go

git commit -m "feat: add flatout-backend skill, backend dev Dockerfile, runner skill copying

- Create flatout-backend OpenCode skill with ConnectRPC/sqlc/goose/TiDB patterns
- Delegate to specialist skills: golang-pro, tidb-sql, sqlc, protobuf
- Update runner to copy ~/.agents/skills into workspace for Kata/Docker agents"

git push origin main
```

On the **runner host**, pull:

```bash
cd /opt/flatout  # wherever your repo lives on the runner host
git pull origin main
```

### Option B — Manual copy (quick test)

If you just want to test without a git push, copy directly to the runner host:

```bash
# Run this ON YOUR WORKSTATION — replace runner-host with the actual host
rsync -avz tools/skills/flatout-backend/ runner-host:~/.agents/skills/flatout-backend/
rsync -avz runner/ runner-host:/opt/flatout/runner/
```

SSH in and verify:

```bash
ssh runner-host
cat ~/.agents/skills/flatout-backend/SKILL.md | head -5
# Expected: ---\nname: flatout-backend\n...
```

---

## Step 2: Ensure the skill is installed on the runner host

On the **runner host**, verify the skill exists at the location OpenCode will read:

```bash
ls -la ~/.agents/skills/
# Expected output:
# golang-pro/
# tidb-sql/
# sqlc/
# protobuf/
# flatout-backend/     <-- should exist
```

If `flatout-backend` is missing, copy it from the repo:

```bash
mkdir -p ~/.agents/skills/flatout-backend
cp -r /opt/flatout/tools/skills/flatout-backend/* ~/.agents/skills/flatout-backend/

# Verify
ls ~/.agents/skills/flatout-backend/SKILL.md
# /home/runner/.agents/skills/flatout-backend/SKILL.md
```

**Important:** The runner's `candidateHomes()` function looks at `$HOME` and `$SUDO_USER` home directories. Make sure the skill is readable by the user that runs the runner process:

```bash
# If the runner runs as user 'flatout':
sudo -u flatout ls ~/.agents/skills/flatout-backend/SKILL.md
```

---

## Step 3: Build the backend developer harness image

On the **runner host**, where Docker is available:

```bash
cd /opt/flatout/runner/harness

# Copy opencode binary (must exist on this host)
cp "$HOME/.opencode/bin/opencode" ./opencode

# Build mcp-bridge
cd mcp-bridge
go build -o ../mcp-bridge-bin ./main.go
cd ..

# Build the stack image using the per-stack Dockerfile under tools/stacks/
cd /opt/flatout/tools/stacks/go-sqlc-mysql
docker build -t devfleet/backend:latest -f Dockerfile .
```

**Expected output:**
```
[harness] Copying opencode binary...
[harness] Building MCP bridge...
[harness] Building Docker image devfleet/backend:latest (backend)...
# ... docker build output ...
[harness] Image built: devfleet/backend:latest
[harness] Done.
```

Verify the image exists:

```bash
docker images | grep devfleet/backend
# devfleet/backend   latest   <hash>   <time>   <size>
```

**If building for containerd/Kata:** Import it after building:

```bash
cd /opt/flatout/tools/stacks/go-sqlc-mysql
IMPORT_CTR=1 docker build -t devfleet/backend:latest -f Dockerfile .
docker save devfleet/backend:latest | sudo ctr -n devfleet-runner images import -
# ... docker save ... | sudo ctr -n devfleet-runner images import -
```

Verify in containerd:

```bash
sudo ctr -n devfleet-runner images list | grep devfleet/backend
# devfleet/backend:latest ...
```

---

## Step 4: Configure the runner to use the backend image

On the **runner host**, edit your runner config. You have two choices:

### Option A — Change the default harness (all tasks use it)

Edit `/opt/flatout/runner/runner.yaml` or `/opt/flatout/runner/runner.docker.yaml` — whichever you use:

```bash
sudo nano /etc/runner/runner.yaml  # or wherever your config lives
```

Add the `execution` section (or modify it):

```yaml
execution:
  mode: auto          # or docker, kata, local
  harness: devfleet/backend:latest
```

**Before:**
```yaml
git:
  ssh_key_path: ""
  pat: ""
```

**After:**
```yaml
git:
  ssh_key_path: ""
  pat: ""

execution:
  mode: auto
  runtime: ""
  harness: devfleet/backend:latest
```

Restart the runner:

```bash
# If running via systemd:
sudo systemctl restart devfleet-runner

# If running in Docker:
cd /opt/flatout/runner
docker rm -f devfleet-runner 2>/dev/null
docker run -d --name devfleet-runner \
  --privileged \
  -v /run/containerd:/run/containerd \
  --mount type=bind,source=/run/netns,target=/run/netns,bind-propagation=rshared \
  -v /var/lib/containerd:/var/lib/containerd \
  -v /dev/kvm:/dev/kvm \
  -v /tmp:/tmp \
  -v /var/lib/runner:/var/lib/runner \
  -v "/opt/flatout/runner/runner.docker.yaml:/etc/runner/runner.yaml:ro" \
  -p 18080:18080 \
  devfleet/runner:latest
```

### Option B — Per-task override (specific tasks use it)

Don't change the config. Instead, send the image in each TaskRequest:

```json
{
  "task_id": "task-001",
  "agent_image": "devfleet/backend:latest",
  "prompt": "Create a new ConnectRPC service for project repositories with CRUD operations..."
}
```

This is useful if you want the default harness for generic tasks but use the backend image only for backend generation tasks.

---

## Step 5: Verify skill discovery works

Send a test task and watch the logs.

### Option A — Use the runner's smoke test

On the **runner host** (or any machine with NATS access):

```bash
cd /opt/flatout/runner
NATS_URL=nats://localhost:4222 go run test/smoke_task.go
```

Or send manually via NATS CLI:

```bash
nats pub devfleet.runner.tasks '{
  "task_id": "test-backend-skill-001",
  "agent_image": "devfleet/backend:latest",
  "prompt": "Write a brief Go function that uses sqlc to query a TiDB table. Return only the function declaration.",
  "timeout_sec": 120
}'
```

Watch the runner logs:

```bash
# If running in Docker:
docker logs -f devfleet-runner | grep -E "copied skills|skill|flatout-backend"

# If running as systemd:
sudo journalctl -u devfleet-runner -f | grep -E "copied skills|skill"
```

### What you should see

**In runner logs (during task startup):**
```
level=INFO msg=copied skills component=runner source=/home/runner/.agents/skills destination=/var/lib/runner/task-xxx/.agents/skills count=6
```

**In OpenCode output (first few JSON lines):**
```json
{"type":"text","part":{"text":"<available_skills>\n  <skill>\n    <name>flatout-backend</name>\n    <description>Backend development for the default Go stack...</description>\n  </skill>\n</available_skills>"}}
```

**In the final task output:**
The response should reference patterns from the skill: `connect.NewError`, `connect.CodeNotFound`, `sql.ErrNoRows`, `repository.Queries`, etc.

### If skills are NOT discovered

| Symptom | Diagnostic | Fix |
|---------|-----------|-----|
| `copied skills count=0` | `~/.agents/skills/` is empty or not readable | Copy skills from repo (Step 2) |
| `no host skills directory found` | Running as different user, or `$HOME` unexpected | Check `candidateHomes()` logic; set `$HOME` explicitly |
| Skill not in `<available_skills>` | OpenCode loaded but didn't scan project skills | Ensure skills are at `/workspace/.agents/skills/**/SKILL.md` inside the container |
| `copied skills` log missing entirely | Running old runner binary | Rebuild and restart runner: `cd runner && go build ./... && ./runner` |

---

## Full End-to-End Test

Here's a complete test you can run on the runner host after completing all steps:

```bash
#!/bin/bash
set -euo pipefail

echo "=== Step 1: Verify skill on host ==="
cat ~/.agents/skills/flatout-backend/SKILL.md | head -3
echo ""

echo "=== Step 2: Verify Docker image exists ==="
docker images | grep devfleet/backend || { echo "Image missing! Build it from tools/stacks/go-sqlc-mysql/Dockerfile"; exit 1; }
echo ""

echo "=== Step 3: Verify runner config ==="
grep -A2 "execution:" /etc/runner/runner.yaml || { echo "No execution section in config"; exit 1; }
echo ""

echo "=== Step 4: Send test task ==="
nats pub devfleet.runner.tasks '{
  "task_id": "e2e-test-'"$(date +%s)"'",
  "prompt": "Write a minimal ConnectRPC Go handler for a GetUser endpoint using sqlc and TiDB. Include the service struct, the handler method with proper error handling, and a one-line comment showing the sqlc query annotation needed. Keep it under 30 lines.",
  "timeout_sec": 60
}'

echo ""
echo "=== Step 5: Watch logs for skill loading ==="
echo "Running: docker logs -f devfleet-runner | grep -E 'copied skills|flatout-backend'"
docker logs -f devfleet-runner 2>&1 | grep -E "copied skills|flatout-backend|error" | head -10
echo ""
echo "Test complete. Check NATS for the result on subject: devfleet.tasks.<task_id>.status"
```

Save as `test_skills_e2e.sh`, make executable (`chmod +x`), and run.

---

## Troubleshooting

### Docker not available in Kata mode

If you use **Kata Containers** (not Docker), you need the image in containerd, not Docker:

```bash
# 1. Build Docker image
cd /opt/flatout/tools/stacks/go-sqlc-mysql
docker build -t devfleet/backend:latest -f Dockerfile .

# 2. Save and import into containerd
docker save devfleet/backend:latest | sudo ctr -n devfleet-runner images import -

# 3. Verify
sudo ctr -n devfleet-runner images list | grep devfleet/backend
```

### Skills directory at non-standard location

If your OpenCode install uses a different skills directory (not `~/.agents/skills/`), update `copySkillsToWorkspace()` in `runner/internal/controller/runner.go`:

```go
// Change this line:
candidate := home + "/.agents/skills"
// To your actual path:
// candidate := home + "/.config/opencode/skills"
```

Then rebuild the runner:

```bash
cd /opt/flatout/runner
go build -o runner ./cmd/runner
```

### OpenCode doesn't see skills even after copy

Check inside the running container:

```bash
# Get the container ID
docker ps | grep flatout

# Check if skills mount worked
docker exec <container-id> ls -la /workspace/.agents/skills/

# Check OpenCode's effective config
docker exec <container-id> cat /workspace/.opencode.json | jq '.skills'
```

If the directory is empty, the `copySkillsToWorkspace()` failed. Check runner logs for the error.

---

## Rollback

If something breaks, revert to the previous harness:

```bash
# Edit config back to default
sudo nano /etc/runner/runner.yaml
# Change: harness: <stack-image>

# Restart runner
sudo systemctl restart devfleet-runner
```

Or build the per-stack image from `tools/stacks/`:

```bash
cd /opt/flatout/tools/stacks/go-sqlc-mysql
docker build -t devfleet/opencode:latest -f Dockerfile .
```

---

## Maintenance: Updating the skill

When you change the server stack (new Go version, new tool, new pattern):

1. **Edit the skill** in the repo:
   ```bash
   nano tools/skills/flatout-backend/SKILL.md
   # or add/references/*.md
   ```

2. **Sync to host** (git pull or rsync)

3. **No rebuild needed** — the runner copies the latest `.md` files at task start. Skills are runtime-loaded, not baked into images.

4. **If you changed tool versions** (Go, buf, sqlc, goose), rebuild the stack image:
   ```bash
   cd /opt/flatout/tools/stacks/go-sqlc-mysql
   docker build -t devfleet/backend:latest -f Dockerfile .
   ```

---

## Summary Table

| Step | Where | Command | Success indicator |
|------|-------|---------|-------------------|
| 1. Sync skill | Workstation + Runner host | `git pull origin main` | `~/.agents/skills/flatout-backend/SKILL.md` exists |
| 2. Install skill | Runner host | `cp -r tools/skills/flatout-backend/* ~/.agents/skills/flatout-backend/` | `ls ~/.agents/skills/` shows 5+ directories |
| 3. Build image | Runner host | `cd tools/stacks/go-sqlc-mysql && docker build -t devfleet/backend:latest -f Dockerfile .` | `docker images \| grep devfleet/backend` |
| 4. Configure | Runner host | Edit `runner.yaml` → add `execution.harness` | Config contains `harness: devfleet/backend:latest` |
| 5. Verify | Runner host | Send NATS task, watch logs | `copied skills count=N` and skill in `<available_skills>` |
