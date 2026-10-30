---
description: Plan a task from docs/plan.md or a GitHub issue using the planner agent
argument-hint: <task text or issue number>
---

Use the `planner` agent to plan: $ARGUMENTS

If the argument is an issue number, read it with `gh issue view $ARGUMENTS`
first. Present the plan and wait for approval before implementing.
