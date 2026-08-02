# Agent Images

Tasks run inside a dev container image selected by `agent_image`. The runner
does not decide where images live; it receives the final Docker image reference
from the server and passes it to `docker run`. If that image is not present on
the host, Docker pulls it using the host's registry credentials.

## Image Sources

Chetter's shared development images are defined in the Git-backed config repo, not in the runner image. The current layout is:

```text
global/images/golang/Dockerfile
global/images/python/Dockerfile
global/images/node/Dockerfile
global/images/rust/Dockerfile
global/images/minimal/Dockerfile
global/images/java-spring/Dockerfile
```

The `chetter-config` GitHub Actions workflow builds those Dockerfiles and publishes tags as `ghcr.io/flatout-works/chetter-agent:<variant>`, for example `ghcr.io/flatout-works/chetter-agent:golang`.

Each variant inherits from `ghcr.io/flatout-works/chetter-agent-base:main`, which is built by the main Chetter CI and contains the shared harness CLIs (`opencode`, `claude-code`, `codewhale`, `pi`, `codex`), `mcp-bridge`, `chetter-entrypoint`, `git`, `gh`, and common runtime tools.

## Image Resolution

Tasks, triggers, and definition YAML can use either fully qualified image refs or short refs:

```yaml
agent_image: ghcr.io/flatout-works/chetter-agent:golang
```

```yaml
agent_image: chetter-agent:golang
```

Set `AGENT_IMAGE_PREFIX=ghcr.io/flatout-works` on the server to make short refs portable. With that setting, the server resolves `chetter-agent:golang` to `ghcr.io/flatout-works/chetter-agent:golang` before storing tasks or handing work to runners. Fully qualified refs such as `ghcr.io/...`, `registry.example.com:5000/...`, and `localhost:5000/...` are left unchanged.

`DEFAULT_AGENT_IMAGE` is used only when a task or trigger omits `agent_image`. It is resolved through the same prefix logic, so either of these work:

```env
DEFAULT_AGENT_IMAGE=chetter-agent:golang
AGENT_IMAGE_PREFIX=ghcr.io/flatout-works
```

```env
DEFAULT_AGENT_IMAGE=ghcr.io/flatout-works/chetter-agent:golang
```

For production, prefer `AGENT_IMAGE_PREFIX=ghcr.io/flatout-works` and short `agent_image` values in config definitions. This keeps team/repo config readable while ensuring any runner host pulls from GHCR instead of looking for local-only tags.

## Available Variants

| Variant | Image Ref With Prefix | Contents |
|---|---|---|
| Golang | `chetter-agent:golang` | Go, buf, sqlc, goose, govulncheck, osv-scanner, hcloud, MySQL client |
| Python | `chetter-agent:python` | Python 3, pip, venv, ruff, mypy, pytest, black, httpx |
| Node.js | `chetter-agent:node` | Node 22, pnpm, TypeScript, ts-node, eslint, prettier |
| Rust | `chetter-agent:rust` | rustup, cargo, clippy, rustfmt, cargo-audit, build-essential, libssl |
| Minimal | `chetter-agent:minimal` | Base harnesses only, no language toolchain |
| Java/Spring | `chetter-agent:java-spring` | JDK 21, Maven, Gradle, Liquibase, PostgreSQL client |

## Creating A Custom Image

Add a new Dockerfile under the appropriate scope in the config repo:

```text
global/images/<variant>/Dockerfile
groups/<team>/images/<variant>/Dockerfile
repos/<owner>/<repo>/images/<variant>/Dockerfile
```

Start from the shared base unless there is a specific reason not to:

```dockerfile
# syntax=docker/dockerfile:1.7
ARG BASE_IMAGE=ghcr.io/flatout-works/chetter-agent-base:main
FROM ${BASE_IMAGE}

RUN apt-get update && apt-get install -y --no-install-recommends \
    my-language-runtime \
    && rm -rf /var/lib/apt/lists/*
```

Update the config repo image workflow if the new scope/path should be built automatically. After GitHub Actions pushes the image, reference it in trigger YAML or task submissions with `agent_image`.

## Image Contract

The runner injects these environment variables into every container:

| Variable | Description |
|----------|-------------|
| `TASK_ID` | Task identifier |
| `WORKSPACE` | Path to the cloned repo (typically `/workspace`) |
| `CHETTER_EXECUTION_ID` | Immutable execution-attempt identifier used for runner and GitHub credential fencing |
| `CHETTER_GITHUB_CREDENTIAL_URL` | Runner-managed private GitHub credential endpoint when the task has a GitHub repository |
| `CHETTER_GITHUB_CREDENTIAL_TOKEN` | Random per-execution capability for the private credential endpoint |
| `HOME` | Set to `/opt/opencode` |
| `XDG_CONFIG_HOME` | Set to `/opt/opencode/.config` |
| `CHETTER_AGENT_NAME` | Agent name from the task request |
| `CHETTER_MODEL_ID` | Resolved LLM model identifier |
| `CHETTER_RUNNER_IMAGE` | Image reference of the runner |
| `CHETTER_RUNNER_IMAGE_DIGEST` | Digest of the runner image |

Secrets (API keys) are forwarded automatically when set in the runner's environment.

## What Is Baked Into Dev Container Images

Dev container images should contain stable runtime tooling: things that are expensive to install, tied to the execution environment, or needed before any task-specific configuration can be fetched.

Today Chetter bakes these into `chetter-agent-base` and derived images:

| Category | Examples |
|---|---|
| Core CLI tooling | `git`, `curl`, `make`, `jq`, `ripgrep`, Docker CLI, MySQL client. |
| GitHub CLI wrapper | `/usr/local/bin/gh` permits an explicit read-only command allowlist and obtains repository-scoped credentials from the execution broker. Arbitrary API access and all writes are blocked and must use Chetter MCP tools. The real binary is at `/usr/local/bin/gh-real`. |
| Language/toolchain packages | Go, buf, sqlc, goose, govulncheck, osv-scanner, hcloud; variant images add Python, Node, or Rust tooling. |
| Agent harnesses | OpenCode, Claude Code, Pi, CodeWhale, `mcp-bridge`, and `chetter-entrypoint`. |
| OpenCode plugin dependencies | npm packages used by built-in OpenCode integrations, including Mem9 support. |
| Current fallback agents | `.opencode/agent/` is copied into runner images today. These are intended to become fallback defaults once Git-backed runtime injection is complete. |

Image rebuilds are still required for toolchain and harness changes. They should not be required for normal prompt, skill, agent, trigger, or model catalog updates once those definitions are managed through `DEFINITIONS_REPO`.

## What Is Injected Per Task Today

Task-specific data is stored by the server, passed to the runner over ConnectRPC, and injected by the runner when it starts the local harness or Docker/gVisor task container.

| Category | Injected values |
|---|---|
| Task content | Prompt, repo URL/ref, timeout, harness name, selected agent name, skill hints, and optional non-secret task env. |
| Workspace mounts | The cloned workspace is mounted at `/workspace`. The runner bridge uses a per-execution HTTP endpoint rather than a mounted socket. |
| Harness config | OpenCode config is generated into the workspace (`/workspace/.opencode.json` and `/workspace/.config/opencode/config.json`) with Chetter MCP and runner bridge MCP entries. |
| Task identity | `TASK_ID`, `WORKSPACE`, `CHETTER_TASK_ID`, `CHETTER_AGENT_SESSION_ID`, `CHETTER_USER_PROMPT_ID`, `CHETTER_EXECUTION_ID`, `CHETTER_AGENT_NAME`, `CHETTER_MODEL_ID`, `CHETTER_RUNNER_IMAGE`, and `CHETTER_RUNNER_IMAGE_DIGEST`. |
| Git identity | The server resolves an agent definition's managed identity when present; otherwise it resolves the team default, then the global default. The runner sets repository-local `user.name` and `user.email` plus `GIT_AUTHOR_*` / `GIT_COMMITTER_*` for every harness mode. |
| Model/provider resolution | The server resolves provider/model/base URL/API-key-env from the active model catalog before the runner starts the task. |
| Runner-owned secrets and provider env | GitHub App credentials are fetched through an execution-scoped broker and are not persisted. A configured `GITHUB_TOKEN` is forwarded only as a temporary compatibility fallback. The runner also forwards provider credentials such as `SYNTHETIC_API_KEY`, `DEEPSEEK_API_KEY`, `OPENCODE_API_KEY`, `ANTHROPIC_API_KEY`, `ZAI_API_KEY`, `GEMINI_API_KEY`, `GROQ_API_KEY`, `XAI_API_KEY`, and `MEM9_*`. User-supplied task env cannot override runner-owned keys. |
| Sandbox/network config | In gVisor mode the task container runs with `--runtime=runsc` and receives proxy env (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`). Proxy settings are operational controls, not a replacement for gVisor's sandbox boundary. |

## Trigger-Type Environment Variables

Webhook-triggered tasks receive these event-specific variables in addition to the standard task identity and runner-owned variables above. References below use shell syntax (`$VAR`) but the agent harness resolves them natively:

| Variable | Trigger type(s) | Description |
|---|---|---|
| `GITHUB_REPO` | `issue`, `pr_review` | Full repository name (e.g. `owner/repo`) |
| `ISSUE_NUMBER` | `issue` | Issue number |
| `ISSUE_TITLE` | `issue` | Issue title text |
| `ISSUE_URL` | `issue` | Issue HTML URL |
| `ISSUE_BODY` | `issue` | Issue body text |
| `ISSUE_ACTION` | `issue` | Webhook action (e.g. `opened`, `labeled`) |
| `COMMENT_BODY` | `issue` | Comment body text (only for `comment` events) |
| `COMMENT_USER` | `issue` | Comment author login (only for `comment` events) |
| `PR_NUMBER` | `pr_review` | Pull request number |
| `COMMENT_AUTHOR` | `pr_review` | User who requested the review via `/chetter-review` |

**Cron triggers** do not inject any trigger-specific environment variables — tasks receive only the standard task identity vars and runner-owned secrets. Pass `GITHUB_REPO` through the trigger prompt (for example `GITHUB_REPO=owner/repo` at the top of the prompt).

`gh` read commands remain available for inspection. GitHub writes from task agents must use the runner-bridge tools (`chetter_create_issue`, `chetter_issue_comment`, `chetter_create_pr`, `chetter_pr_review`) so canonical footers, audit events, and task artifact records are created consistently.
