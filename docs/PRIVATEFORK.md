# Maintaining A Private Chetter Fork

Organizations can track the public Chetter repository while adapting it as a
private internal tool. Use a private standalone clone with an `upstream` remote
rather than a GitHub fork: forks of public repositories are generally public,
while a standalone private repository keeps branches, pull requests, CI
configuration, and secrets private.

## Initial Setup

Create a local clone, rename the public remote to `upstream`, and push it to a
new private repository:

```bash
git clone https://github.com/flatout-works/chetter.git chetter-private
cd chetter-private
git remote rename origin upstream
git remote add origin git@github.com:company/chetter.git
git push -u origin main
```

Keep `upstream` read-only and use `origin` for the private company repository.

## Recommended Workflow

1. Protect the private `main` branch and make company changes through pull requests.
2. Keep deployment configuration and organization-specific customizations in separate repositories where practical, minimizing divergence from Chetter upstream.
3. Periodically merge upstream changes through a dedicated sync pull request:

```bash
git fetch upstream
git switch -c sync/upstream-main origin/main
git merge upstream/main
git push -u origin sync/upstream-main
```

4. Open a private pull request from `sync/upstream-main` to `main`, resolve conflicts, run the relevant checks, and merge normally.
5. Contribute generally useful fixes upstream where possible instead of carrying them privately.

Avoid rebasing or force-pushing the private `main` branch. Syncing upstream via
reviewed merge pull requests preserves a clear history and makes conflicts
manageable.
