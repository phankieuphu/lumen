---
description: Verify, review, and split the current work into Conventional Commits
---

1. Run `make check` and fix failures.
2. Run the `reviewer` agent on `git diff main...HEAD` plus uncommitted changes;
   fix blockers and majors.
3. Propose a commit split (one logical change each, Conventional Commits per
   `docs/conventions.md` §8) and wait for approval before committing.
