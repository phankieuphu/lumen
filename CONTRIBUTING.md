# Contributing

## Setup

1. Go 1.26+, Docker, and `make`.
2. `make tools` installs `golangci-lint`, `gofumpt`, and `govulncheck`.
3. `make db-up` starts PostgreSQL and MySQL; `make test-int` runs integration
   tests against them.

## Workflow

1. Open or pick an issue. Larger changes start with a design note in the issue.
2. Branch from `main`: `feat/<short-desc>` (see
   [conventions §9](docs/conventions.md#9-branches-and-pull-requests)).
3. Commit with Conventional Commits, one logical change per commit.
4. Before pushing: `make check` (fmt, lint, test, vuln).
5. Open a PR with the template filled in. CI must be green.

All rules live in [docs/conventions.md](docs/conventions.md).
