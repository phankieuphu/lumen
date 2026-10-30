# CLAUDE.md

@AGENTS.md

## Claude Code specifics

- Sub-agents live in `.claude/agents/`:
  - `planner` — turns a plan task or issue into an implementation plan. Read-only.
  - `go-backend` — implements Go changes in `internal/` following the conventions.
  - `reviewer` — reviews a diff for security, performance, concurrency, and convention issues. Read-only.
  - `test-writer` — adds table-driven unit tests and `integration`-tagged engine tests.
- Slash commands live in `.claude/commands/` (`/plan-task`, `/ship`).
- When you finish a task from `docs/plan.md`, tick its checkbox in the same PR.
- Prefer editing existing files over creating new ones; don't create new
  top-level docs without being asked.
