# PR Review Workflow

## When to use this skill

Use this skill for Chetter pull request review orchestration, standard review, adversarial review, and synthesis tasks.

## Shared Rules

- Work in English.
- Verify the PR head SHA before relying on results.
- Treat the current code as the source of truth.
- Review linked issues and PR design notes when they define the required behavior.
- Use file and line references for findings whenever possible.
- Distinguish blockers from non-blocking findings.
- Never claim a review round happened unless it produced a visible artifact or a completed task export.
- Do not merge, close, or push to the reviewed PR.

## Tooling Rules

- Use `gh` for read-only GitHub inspection.
- Use Chetter MCP tools for Chetter task operations and GitHub write operations.
- Review child tasks must not post to GitHub directly.
- The synthesizer posts exactly one final PR review with `chetter_pr_review`.

## Quality Bar

The goal is to find real defects, not to rubber-stamp the PR. A correct PASS requires evidence: inspected files, checked requirements, and targeted verification. If there are no blockers, still report meaningful residual risks and test gaps.
