# Task template: code improvements

This file in `global/task-templates/*.md` defines a global reusable task prompt template.

Perform a focused improvement pass on the target repository:

1. Review the codebase for common issues:
   - missing error context or logging
   - inconsistent patterns
   - unclear naming
   - duplicated logic

2. Keep changes minimal and behavior-preserving. Do not change public APIs,
   data formats, database schemas, generated files, or unrelated code.

3. Run the repository's tests before committing.

4. If changes are made, create a branch, commit, push, and open a pull request
   against the main branch.

5. Add the Chetter footer to any created PRs.
