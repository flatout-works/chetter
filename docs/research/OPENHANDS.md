# OpenHands Architecture Analysis

Status: **Reference — insights for Chetter development**

Based on analysis of [OpenHands](https://github.com/OpenHands/OpenHands) (the Agent Canvas — management/orchestration layer for coding agents). June 2026.

---

## Overview

OpenHands is a self-hosted control plane for coding agents. It manages sandboxed execution environments, conversation lifecycle, LLM configuration, secrets, multi-provider git integration, and a skill/plugin system. The Canvas (FastAPI) communicates with per-sandbox Agent Servers via HTTP + webhooks.

---

## Key Concepts Relevant to Chetter

### 1. Sandbox Service Abstraction (`SandboxService` interface)

OpenHands abstracts execution backends behind `SandboxService` with three implementations: Docker containers, direct subprocess, and remote API. Each sandbox gets health checks, exposed URLs (agent server, VSCode), dynamic port mapping, and pause/resume lifecycle.

**Chetter angle:** Chetter bakes Docker/gVisor directly into the runner model. Abstracting behind an `ExecutionBackend` interface would support local process (for dev), Docker, remote VMs, and Kubernetes without changing the control plane.

### 2. Multi-Step Conversation Setup Pipeline

OpenHands has a structured async pipeline: clone repo → run `.openhands/setup.sh` → install git hooks → load skills from multiple sources → create agent → start conversation. Each step has tracked status progression.

**Chetter angle:** Chetter's task creation is simple (insert DB record, poll for claim). A richer setup pipeline could clone repos, run project-level init scripts, and load skills before the agent starts, enabling per-project customization via `.opencode/` config.

### 3. Multi-Source Skill/Plugin Loading

Skills are loaded and merged from five sources: public repo (`OpenHands/skills`), user dir (`~/.openhands/skills/`), org repo, project repo (`.agents/skills/`, `.openhands/microagents/`), and sandbox container. Deduplicated by name.

**Chetter angle:** Chetter has `.opencode/agent/` definitions but no dynamic skill loading. A similar multi-source loader would let project repos ship `.opencode/skills/` with domain-specific tools, instructions, and hooks.

### 4. LLM Profile Management

Saved model configurations with runtime switching. Users can define multiple profiles (different models, API keys, parameters) and switch without restarting the server.

**Chetter angle:** Chetter's model config is entirely env-var driven. A profile system with runtime switching would let users experiment with models and providers without server restarts.

### 5. Multi-Provider Git Integration (`GitService` interface)

Supports GitHub, GitLab, Bitbucket, Bitbucket Data Center, Azure DevOps, Forgejo, and Jira DC via a common `GitService` interface. Provider auto-detection from repo URL. PR/MR creation exposed as MCP tools.

**Chetter angle:** Chetter only supports GitHub. The same `GitService` abstraction pattern would enable GitLab/Bitbucket/Azure DevOps support with minimal per-provider code.

### 6. Event Callback System

Generic `EventCallbackProcessor` pattern — any event kind (conversation created, message received, tool used) can trigger registered processors. Built-in examples: `LoggingCallbackProcessor`, `SetTitleCallbackProcessor`. Callbacks stored in SQL DB.

**Chetter angle:** Chetter's trigger system (webhook → create task) could be extended to fire on any task lifecycle event (started, completed, failed, artifact-created), enabling more flexible automation.

### 7. Secrets Validation & Multi-Source Merge

`SecretsStore` validates secrets against: `BLOCKED_SECRET_NAMES` (critical env vars that cannot be overridden), `BLOCKED_SECRET_PREFIXES` (`LLM_*` reserved), max count/name/value length limits. Secrets merged from API, DB, and provider tokens.

**Chetter angle:** Chetter has basic token storage. Validation rules prevent user-supplied secrets from overriding system config — a simple safety net worth adopting.

### 8. DI Framework (`Injector[T]`)

Every major service has an `Injector` subclass that yields a service instance from request state. `InjectorState` carries per-request context. Makes every service swappable via config.

**Chetter angle:** Chetter has no DI. A lightweight injector pattern would make the service layer more testable and make it easy to swap implementations (e.g., different event stores, sandbox backends).

### 9. Error Classification

Categorizes conversation failures: `budget_exceeded`, `model_error`, `runtime_error`, `timeout`, `user_cancelled`. Used for analytics tracking and user-facing error messages.

**Chetter angle:** Chetter doesn't classify task failures. Adding error categories would improve observability and operations — you can detect if a model provider is failing vs. budgets being hit vs. tasks timing out.

---

## Summary Table

| Pattern | OpenHands | Chetter Today | Suggested Direction |
|---------|-----------|---------------|-------------------|
| **Sandbox backend** | `SandboxService` interface (Docker/Process/Remote) | Concrete Docker + gVisor | Extract `ExecutionBackend` interface |
| **Conversation setup** | Multi-step async pipeline (clone → setup → hooks → skills → start) | Simple DB insert + poll | Add setup pipeline with per-project `.opencode/` config |
| **Skill loading** | 5 sources merged & deduplicated | `.opencode/agent/` only | Multi-source skill loader |
| **LLM config** | Saved profiles, runtime switching | Env vars only | Profile system with API support |
| **Git providers** | `GitService` with auto-detection (7 providers) | GitHub only | `GitService` interface |
| **Event callbacks** | Generic processor pattern + SQL storage | Hardcoded webhook triggers | Extend trigger system to task lifecycle |
| **Secrets** | Validation (blocked names, size limits), multi-source merge | Basic token storage | Add validation rules |
| **DI** | `Injector[T]` for all services | None | Lightweight DI for testability |
| **Error classification** | 5 categories (budget, model, runtime, timeout, cancel) | None | Classify task failures |
| **File storage** | `FileStore` interface (Local/S3/GCP/Memory) | Filesystem only | Abstract for cloud backends |
