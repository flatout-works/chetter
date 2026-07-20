# Why Chetter Instead Of Only GitHub Actions?

GitHub Actions can run Claude Code, OpenCode, Codex, or another agent CLI in response to a pull request. For a single, short-lived, repository-local PR review workflow, that may be all that is needed.

Chetter is useful when the goal is to operate agents as a shared, observable, secure service rather than as one-off CI jobs. It does not replace GitHub Actions for builds, tests, releases, or other deterministic CI work. It provides the control plane and runner fleet for long-running and interactive agent workloads, including PR review.

## Advantages

1. **Use standard agent harnesses without tying the workflow to one CI implementation.**

   Chetter runs standard CLIs including Claude Code, OpenCode, Codex, Pi, and CodeWhale. A review policy can choose the harness, model, variant, image, timeout, and skills independently of a repository workflow file. The same task definition can be used for PR reviews, scheduled work, issue responders, and manually submitted work.

2. **Keep agent infrastructure and credentials out of every repository.**

   With Actions-only automation, each repository normally needs workflow YAML, secrets, permissions, action versions, and maintenance. Chetter centralizes runner configuration, provider credentials, GitHub App integration, and policy while allowing teams to use scoped tokens and trigger configuration. Repositories can opt in to an approved review service instead of each becoming its own agent platform.

3. **Run purpose-built environments, not a generic CI VM.**

   Chetter agents run in Docker or Kubernetes containers selected per task. Teams can build stack-specific images containing their actual compilers, linters, SDKs, database clients, and review tools. Optional gVisor isolation provides a stronger sandbox boundary than simply giving an agent a hosted CI runner.

4. **Operate a durable runner fleet.**

   Chetter uses runners that register, poll, heartbeat, and claim work with renewable leases. Expired work is reaped and retried, fleet health is visible, and runners can be drained during rollout. This is useful when agent workloads are slow, bursty, costly, or need controlled concurrency, instead of consuming a fresh CI runner for every invocation.

5. **Keep a database-backed record of every agent run.**

   Chetter persists tasks, triggers, runs, sessions, session runs, task events, progress, terminal results, and created GitHub artifacts in its database. Session transcripts can be exported for retrospective analysis, prompt and agent improvement, quality evaluation, and compliance evidence. Operators can inspect an in-progress review, diagnose a stuck agent from its heartbeat or event history, and recover failed work. GitHub Actions provides useful job logs, but it is not a cross-workload agent session and task observability system.

6. **Support long-running and resumable agent work.**

   Agent tasks can use long timeouts and can be paused and resumed with follow-up prompts on the same runner. That enables work such as: investigate a review finding, continue after human feedback, complete a multi-stage migration, or let an agent refine a PR. Actions jobs are fundamentally ephemeral workflow runs; reproducing continuation requires building that state management yourself.

7. **Provide a consistent interface beyond PR events.**

   PR reviews are one trigger type. Chetter also supports cron triggers, GitHub issue/comment triggers, generic task submission, task lifecycle callbacks, and direct MCP or API use from developer tools. A team can start with a reviewer and later use the same platform for dependency audits, backlog triage, incident investigation, release checks, or agent-authored fixes.

8. **Make review behavior centrally configurable and composable.**

   Chetter's PR-review triggers are configured independently from a repository's CI pipeline. Multiple triggers can review the same PR, for example a deep correctness reviewer and a security reviewer, each with its own prompt, agent, model, image, and timeout. It supports label, fork, and authorized `/chetter-review` comment paths, plus direct manual submission.

9. **Create GitHub artifacts through controlled, attributable tools.**

   Chetter offers server-side tools to create issues, comments, PRs, and reviews. These attach a canonical task signature, write audit records, and track resulting artifacts. This gives an operator a way to trace which task created a GitHub change instead of distributing broad `gh` write credentials to every agent container.

10. **Apply team boundaries, auditing, and governance.**

    Chetter supports team-scoped tokens, task and trigger attribution, audit logs, artifact tracking, and usage summaries. The audit trail covers operational actions such as task submissions, cancellations, trigger changes, and GitHub artifact creation. These are important when several teams share agent infrastructure or when model spend, permissions, and agent output need reviewable ownership.

11. **Avoid CI-platform lock-in for agent execution.**

    Chetter integrates deeply with GitHub, but its runners, task API, and MCP interface are independent of GitHub Actions. GitHub can remain the source of PR events and the place where reviews appear, while the actual agent runtime can run on infrastructure controlled by the organization.

12. **Let GitHub Actions do what it is best at.**

    Actions remains a strong fit for deterministic checks: build, test, lint, package, deploy, and small scripted automation. Chetter complements it for non-deterministic, stateful, tool-using work where an agent needs a richer environment, more time, operational visibility, or a follow-up conversation.

13. **Control and monitor agents through MCP tools and a web UI.**

    Developers and automation clients can submit tasks, inspect status and progress, retrieve transcripts, cancel work, resume sessions, and manage triggers through a standard MCP interface. The web UI provides a shared operational view of tasks, sessions, artifacts, triggers, runner health, and administration. This makes agents manageable during execution, rather than requiring operators to infer state from individual workflow runs and logs.

## When Actions Alone Is Enough

Use only GitHub Actions when all of the following are true:

- You need one simple PR-triggered agent invocation.
- The task completes reliably within normal CI time limits.
- A hosted runner or a basic self-hosted runner has the required tools.
- Repository-local workflow configuration and secrets are acceptable.
- GitHub job logs and status checks provide sufficient observability.
- There is no need to resume work, share a fleet, centralize governance, or reuse the agent workflow outside GitHub events.

## Practical Position

The argument is not that GitHub Actions cannot run an AI PR reviewer. It can. The relevant question is whether the organization wants to own and repeatedly operate agentic workloads.

If PR review is a single experiment, an Actions workflow is the smallest solution. If it is the first of many agent workflows, or must be secure, observable, configurable, resumable, and shared across teams, Chetter removes the control-plane work that would otherwise have to be built and maintained around GitHub Actions.

## Twenty Useful Agent Automations

These examples are appropriate for Chetter because they benefit from a real development environment, substantial context gathering, long execution time, a reusable agent definition, human follow-up, or centrally retained results.

1. **Nightly vulnerability checks**: scan the current dependency tree and container images, triage new findings, compare against an approved exception list, and create or update a report or issue.

2. **Deep PR review**: examine a PR for correctness, security, performance, error handling, and missing tests, then publish a structured GitHub review.

3. **Service rulebook review**: evaluate a service against the organization's operational and engineering rulebook, then produce a Markdown report for the knowledge base.

4. **Changelog maintenance**: inspect merged changes since the last release, draft categorized release notes, and open a PR for human review.

5. **Documentation updates**: detect documentation affected by code, configuration, or API changes; update the relevant guides; and open a documentation PR.

6. **Dependency upgrade planning**: identify outdated or vulnerable dependencies, read their release notes and breaking changes, estimate the migration work, and create a prioritized upgrade plan.

7. **Dependency upgrade implementation**: take an approved upgrade issue, update dependencies and affected code, run the relevant tests, and open a PR with the evidence.

8. **Repository health audit**: periodically review test coverage signals, failing or flaky checks, stale TODOs, dead configuration, dependency age, and ownership gaps, then publish a prioritized report.

9. **Test-gap analysis**: inspect recent code changes or a subsystem, identify meaningful untested behavior and edge cases, and propose or implement focused tests in a PR.

10. **Flaky-test investigation**: trigger from repeated CI failures, gather failed-run logs and relevant history, attempt to reproduce the failure in a controlled environment, and produce a diagnosis with a proposed fix.

11. **Issue triage and enrichment**: classify newly opened issues, find duplicates and related code, request missing reproduction details where appropriate, apply labels, and create a concise implementation brief.

12. **Backlog refinement**: periodically inspect stale or underspecified issues, validate that they still matter, add technical context and acceptance criteria, and close or relabel obsolete work.

13. **Incident investigation**: trigger from an alert or manually submitted incident prompt, collect repository context, deployment changes, logs supplied by the operator, and runbook guidance, then produce a time-stamped investigation report.

14. **Post-incident action tracking**: turn an approved incident report into concrete follow-up issues or PRs, connect each action to evidence, and periodically report on incomplete remediation work.

15. **Security rule or policy review**: assess code and deployment configuration against organization-specific security rules, distinguish actionable violations from approved exceptions, and publish an auditable report.

16. **Infrastructure-as-code review**: examine Terraform, Kubernetes, Compose, Helm, or deployment changes for unsafe defaults, missing resource limits, exposure risks, rollout hazards, and drift from platform standards.

17. **Release-readiness review**: before a release, check the release branch for version consistency, migration notes, changelog completeness, test evidence, known risks, rollback instructions, and unresolved blockers.

18. **API compatibility review**: analyze changes to protobuf, OpenAPI, database schema, or public interfaces for backward compatibility, client impact, migration needs, and documentation requirements.

19. **Codebase modernization proposals**: inspect a selected area for deprecated libraries, obsolete patterns, unsupported runtimes, or maintainability risks, then create a staged proposal with estimated scope and safe migration order.

20. **Knowledge-base maintenance**: synthesize completed PRs, incident reports, service reviews, and recurring operational findings into updated runbooks, architecture notes, decision records, or FAQ entries for human approval.

For each automation, a Chetter trigger can specify its agent, model, prompt, image, skills, timeout, repository, and schedule or webhook event. Start with reports and human-approved PRs; allow agents to make direct changes only after the workflow has demonstrated reliable results.
