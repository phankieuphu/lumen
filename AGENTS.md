# AGENTS.md

Guidance for AI coding agents (Claude Code, Codex, Cursor, …) working in this
repository. Humans should read [CONTRIBUTING.md](CONTRIBUTING.md) first.

## Project

kiemdb (working name) — desktop SQL client. Go 1.26 backend in `internal/`,
Wails v3 shell in `ui/shell`, React + TypeScript frontend in `ui/frontend`.
The roadmap is [docs/plan.md](docs/plan.md); the layout and query path are in
[docs/architecture.md](docs/architecture.md).

## Commands

```sh
make build        # go build ./...
make test         # unit tests with -race
make test-int     # integration tests (needs `make db-up`)
make lint         # golangci-lint
make fmt          # gofumpt + goimports
make check        # fmt-check + lint + test + vuln — run before every commit
```

Run a single test: `go test -race -run 'TestClassify/cte' ./internal/query/...`

## Hard rules

Read [docs/conventions.md](docs/conventions.md). The non-negotiables:

1. `ui/shell` is the only package that imports Wails. `internal/` never imports `ui/`.
2. Never log, persist, or return secrets or DSNs. Passwords go through `internal/secret`.
3. The statement classifier fails closed: unknown → write.
4. Read-only mode is enforced in both the classifier and the engine session.
5. The audit log is append-only and hash-chained. Never add update/delete paths.
6. Stream result rows in pages with a max-row cap. Never buffer a whole result.
7. Every goroutine has an owner that stops and waits for it.
8. Engine behaviour is tested against real databases (build tag `integration`), not mocks.
9. Conventional Commits, one logical change per commit, each commit green.
10. Don't add dependencies without stating why in the PR.

## Workflow for agents

1. **Plan** — for anything beyond a one-file fix, use the `planner` agent (or
   write a short plan) listing packages touched, interfaces changed, tests to
   add, and the phase task in `docs/plan.md` it ticks.
2. **Implement** — follow existing patterns in the nearest package. Keep
   changes scoped to the plan.
3. **Verify** — `make check`; for engine code also `make test-int`.
4. **Review** — run the `reviewer` agent on the diff before opening a PR.
5. **Commit** — split by package/concern; message format in conventions §8.
