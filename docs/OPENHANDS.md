# OpenHands Architecture Analysis

Status: **Reference — insights for Chetter development**

Based on analysis of [OpenHands](https://github.com/OpenHands/OpenHands) (the Agent Canvas — management/orchestration layer for coding agents). June 2026.

## Overview

OpenHands is a self-hosted control plane for coding agents. It manages sandboxed execution environments, conversation lifecycle, LLM configuration, secrets, multi-provider git integration, setup pipelines, hooks, and a skill/plugin system.

The current OpenHands architecture is split into an app-server/control-plane and per-sandbox agent servers. The app server orchestrates setup, auth, config, git providers, and persistence. The agent server runs inside a sandbox and executes the actual conversation loop through the OpenHands SDK.

Chetter has a similar high-level split, but at a different abstraction level: the Chetter server is the control plane, while Chetter runners execute external harnesses such as OpenCode, Pi, and Claude Code. OpenHands is both an orchestrator and an agent runtime; Chetter should remain primarily an orchestrator and let harnesses own the fine-grained agent loop.

## Key Concepts Relevant to Chetter

### 1. Sandbox Service Abstraction

OpenHands abstracts execution backends behind a `SandboxService` with Docker, subprocess, and remote API implementations. Each sandbox gets health checks, exposed URLs, dynamic port mapping, and pause/resume lifecycle.

OpenHands also has sandbox grouping strategies: no grouping, newest sandbox, least recently used, fewest conversations, or add to any available sandbox.

**Chetter angle:** Chetter bakes Docker/gVisor directly into the runner model. An `ExecutionBackend` interface would support local process execution for development, Docker/gVisor for production, and eventually remote VMs or Kubernetes without changing the control plane. Sandbox grouping is less relevant today because Chetter models concurrency at the runner level rather than sharing a sandbox across conversations.

### 2. Multi-Step Setup Pipeline

OpenHands has a structured async pipeline:

```text
WORKING -> WAITING_FOR_SANDBOX -> PREPARING_REPOSITORY -> RUNNING_SETUP_SCRIPT -> SETTING_UP_GIT_HOOKS -> SETTING_UP_SKILLS -> STARTING_CONVERSATION -> READY
```

The pipeline clones the repo, runs `.openhands/setup.sh`, installs `.openhands/pre-commit.sh`, loads skills from multiple sources, builds the agent, and starts the conversation.

**Chetter angle:** Chetter currently inserts a task and the runner handles clone/execution. A richer runner-side setup pipeline could report phases like `setup.cloning`, `setup.script`, `setup.skills`, and `task.started`, giving better progress visibility and enabling per-project customization through `.opencode/` or `.chetter/` config.

### 3. Multi-Source Skill/Plugin Loading

OpenHands loads and merges skills from public skills, user skills, organization config repos, project repos, and sandbox-provided skills. Skills are Markdown files with optional triggers. Later sources override earlier ones by name.

Trigger types include keyword triggers, slash-command task triggers, and always-loaded repo skills.

**Chetter angle:** Chetter ships fixed skills in runner images and passes skill names to harnesses. It would be useful to load project-local `.opencode/skills/`, org-level skills, and built-in skills, deduplicate by name, and pass the merged result to the harness.

### 4. LLM Profile Management

OpenHands persists named LLM profiles and supports runtime model switching. Profiles include model, base URL, API key handling, reasoning parameters, and usage identifiers. Runtime switching uses a cache-busting usage ID so the agent server rebinds to the requested LLM.

**Chetter angle:** Chetter has a model catalog and resolves provider/model at claim time. A team-scoped LLM profile system would let triggers reference profile names instead of raw provider/model fields and make experimentation safer.

### 5. Multi-Provider Git Integration

OpenHands supports GitHub, GitLab, Bitbucket, Bitbucket Data Center, Azure DevOps, Forgejo, and Jira DC through a common `GitService` interface. Provider detection tries the specified provider first and then available provider tokens.

**Chetter angle:** Chetter currently has GitHub-specific webhook and artifact tooling. A `GitService` interface would enable GitLab/Bitbucket/Azure DevOps support without embedding provider logic throughout the service layer.

### 6. Event Callback System

OpenHands has a generic `EventCallbackProcessor` model. Callbacks are registered per conversation and dispatched by event kind. Built-in processors include logging and automatic title setting. Event callbacks are stored in SQL and can complete, disable themselves, or fail.

**Chetter angle:** Chetter already records task events and has an in-memory event bus for streaming. A SQL-backed event callback system could let events trigger multiple action types: spawn a new task, send a webhook, post to Slack, notify PagerDuty, or record analytics. This is one of the strongest OpenHands-inspired ideas for Chetter.

### 7. Secrets Validation and Multi-Source Merge

OpenHands validates secrets against blocked exact names, blocked prefixes, count limits, name limits, and value-size limits. It blocks overriding runtime-critical env vars and LLM control variables while allowing expected provider tokens.

**Chetter angle:** Chetter accepts task environment JSON. It should validate env var names and block prefixes such as `CHETTER_` and `LLM_` so user-supplied task env cannot override control-plane or harness configuration.

### 8. Dependency Injection Pattern

OpenHands uses injectors to resolve service implementations from request state and config. This makes major services swappable.

**Chetter angle:** Chetter does not need a large DI framework, but small interfaces around event dispatch, git providers, file storage, and execution backends would improve testing and modularity.

### 9. Error Classification

OpenHands categorizes failures as budget exceeded, model error, runtime error, timeout, or user cancelled. These categories are used for analytics and user-facing messages.

**Chetter angle:** Chetter currently stores status and raw error text. Adding an `error_category` field would improve operations: dashboards can distinguish model provider issues from runtime errors, timeouts, cancellations, budget limits, or stuck agents.

### 10. Conversation State Machine

OpenHands conversations use richer execution states:

```text
IDLE, RUNNING, PAUSED, WAITING_FOR_CONFIRMATION, FINISHED, ERROR, STUCK, DELETING
```

`WAITING_FOR_CONFIRMATION` lets a user approve or reject pending actions. `STUCK` is set by loop detection. `PAUSED` supports interruption and resume.

**Chetter angle:** Chetter's task state machine should remain simpler because harnesses own the agent loop. However, the ideas map to task/session features: `waiting_confirmation` for human approval, `stuck` as an error category, and richer session pause reasons.

### 11. Agent Execution Loop

OpenHands owns the full LLM/tool loop: prepare messages, call the LLM, classify response, execute tool calls in parallel, append observations, check finish/budget/iteration/stuck conditions, and loop.

**Chetter angle:** Chetter should not copy this loop. The Chetter runner delegates to harnesses. The useful lesson is not to implement a second agent runtime, but to make the harness interface report more structured progress, errors, cost, and phase data.

### 12. Budget and Iteration Limits

OpenHands has max iterations and max budget per run. Cost is tracked across all LLMs used by a conversation, including agent, condenser, and critic. Budget exhaustion emits a structured error.

**Chetter angle:** Add `max_budget_usd` and optionally `max_iterations` to tasks later. Even before strict enforcement, recording token/cost metrics from harness events would be valuable.

### 13. Confirmation Mode

OpenHands supports `NeverConfirm`, `ConfirmRisky`, and `AlwaysConfirm`. Risky actions can be flagged by an LLM security analyzer. Pending actions wait for explicit approval or rejection.

**Chetter angle:** This is useful for enterprise deployments but requires harness cooperation. It should be a later feature after the event system can represent pending actions and callbacks can notify humans.

### 14. Hooks System

OpenHands loads hooks from project config. Hook events include pre-tool-use, post-tool-use, user-prompt-submit, session-start, session-end, and stop. Hooks can block messages or actions.

**Chetter angle:** A `.opencode/hooks.json` or `.chetter/hooks.json` system could enforce repository policies before risky actions, before PR creation, or before task completion. This is most practical if implemented through harness configuration rather than by Chetter parsing every tool call itself.

### 15. Stuck Detection

OpenHands detects repeated action/observation pairs, repeated action errors, monologues, and alternating patterns.

**Chetter angle:** Chetter currently relies on timeouts and leases. Runner-side stuck detection could classify failures as `stuck` and stop wasting tokens earlier.

### 16. Context Condensation

OpenHands condenses context when the LLM context window is exceeded or conversation history becomes malformed. It can also force condensation via API.

**Chetter angle:** This should remain harness-owned. Chetter can surface condensation events if the harness reports them, but should not own prompt compaction.

### 17. Goal Loops and Sub-Tasks

OpenHands has goal-loop endpoints for running focused sub-goals within a conversation and supports sub-agents for parallel work.

**Chetter angle:** The closest equivalent is parent/child tasks. A future `chetter_spawn_task` tool could let a task create dependent subtasks, then merge results back into the parent. This is powerful but substantially more complex than the current queue model.

### 18. Webhook Push From Agent Server

OpenHands agent servers push lifecycle and event-stream updates to the app server via authenticated webhooks.

**Chetter angle:** Chetter's ConnectRPC polling model is simpler and robust for runners behind NAT. Keep it. The useful inspiration is the event vocabulary, not the transport mechanism.

### 19. MCP Proxy Pattern

OpenHands proxies MCP tools through the app server so sandboxes can use external services without directly holding secrets.

**Chetter angle:** Chetter already exposes a server MCP surface and a per-task runner MCP surface. A future improvement would let tasks mount additional configured MCP servers, with secrets resolved by the server.

### 20. File Store Abstraction

OpenHands has local, S3, GCP, and memory file stores.

**Chetter angle:** If Chetter stores session exports, checkpoints, artifacts, or logs beyond the local runner, a file-store abstraction would make cloud backends easier.

### 21. Org-Level Configuration Repositories

OpenHands resolves organization config repositories with provider-specific conventions. Org skills and config then apply to all repos under that org.

**Chetter angle:** This maps well to team-scoped defaults: org skills, default model profiles, hooks, and event callbacks shared across repositories.

### 22. Event Actions Beyond Task Creation

OpenHands callbacks are not limited to starting new work. They can update metadata, perform logging, or run arbitrary processors.

**Chetter angle:** Event triggers should support multiple action types, especially `create_task` and `webhook`. Generic webhooks can cover Slack, Teams, Discord, PagerDuty, or internal automation.

## Prioritized Recommendations

### Phase 1: Quick Wins

1. Add task error classification (`error_category`).
2. Validate task environment variables and block dangerous prefixes.
3. Record more granular event types alongside existing task events.

### Phase 2: Event-Driven Automation

1. Add SQL-backed event callbacks.
2. Support callback action types: `create_task` and `webhook`.
3. Add MCP tools to list/create/update/delete event callbacks.
4. Emit event types using dot notation, such as `task.completed`, `task.failed.model_error`, and `artifact.created`.

### Phase 3: Agentic Flow Enhancements

1. Add runner-side setup phases and project setup scripts.
2. Load project-level and org-level skills.
3. Add stuck detection.
4. Explore confirmation mode if the primary harness supports it cleanly.

### Phase 4: Larger Strategic Work

1. Add Git provider abstraction.
2. Add LLM profiles.
3. Add parent/child task support.
4. Add file-store abstraction for exports/checkpoints/artifacts.

## Compatibility Notes

Some OpenHands patterns should not be copied directly:

| Pattern | Recommendation |
| --- | --- |
| Agent execution loop | Do not copy. Chetter delegates this to harnesses. |
| Rich conversation state machine | Adopt selected concepts only: confirmation, stuck, pause reasons. |
| Webhook push transport | Keep ConnectRPC polling. It is simpler and better for runner deployments. |
| Sandbox grouping | Not needed while Chetter concurrency is runner-based. |
| Full sub-agent runtime | Consider parent/child tasks later, not SDK-level sub-agents now. |

## Summary Table

| Pattern | OpenHands | Chetter Today | Suggested Direction |
| --- | --- | --- | --- |
| Sandbox backend | `SandboxService` interface | Concrete runner Docker/gVisor | Extract `ExecutionBackend` later |
| Setup pipeline | Structured clone/setup/hooks/skills/start phases | Runner clone + harness start | Add runner setup phases |
| Skill loading | Five sources, triggers, dedupe | Built-in image skills + names | Multi-source skill loader |
| LLM config | Saved profiles, runtime switching | Catalog + task fields | Team-scoped profiles |
| Git providers | Common `GitService` | GitHub only | Provider interface |
| Event callbacks | Generic SQL-backed processors | Event log + streaming bus | Event callbacks with actions |
| Secrets | Validation and merge | Task env JSON | Env validation and blocked prefixes |
| DI | Injector pattern | Direct service wiring | Small interfaces where useful |
| Error classification | Categorized failures | Raw error text | Add `error_category` |
| Conversation states | Pause, confirmation, stuck | Task/session statuses | Add selected concepts only |
| Budget limits | Cost + iteration limits | Timeout only | Track and enforce later |
| Hooks | Project hook config | None | Harness-integrated hooks later |
| Stuck detection | Loop pattern detector | Timeout only | Runner-side detector later |
| MCP proxy | Server-mediated external MCP | Server MCP + runner MCP | Configurable task MCP servers |
| File storage | Local/S3/GCP/memory | Local/db paths | File store abstraction later |
| Event actions | Arbitrary processors | Triggers spawn tasks | Add webhook/Slack actions |
