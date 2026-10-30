# lumen

A desktop SQL client for PostgreSQL, MySQL, and SQLite with an audit log, query
explain/debug tools, an AI assistant, a slow-query advisor, and a lock monitor.

- **Backend:** Go 1.26, one module, core in `internal/` (testable without the UI)
- **Shell:** Wails v3, isolated in `ui/shell`
- **Frontend:** React + TypeScript (`ui/frontend`)

## Quick start

```sh
make tools      # install golangci-lint, gofumpt, govulncheck
make db-up      # PostgreSQL 17 + MySQL 8.4 in Docker
make test       # unit tests
make test-int   # integration tests against the containers
make lint
```

## Docs

| Doc | What it covers |
| --- | --- |
| [docs/plan.md](docs/plan.md) | Phases, milestones, tasks |
| [docs/architecture.md](docs/architecture.md) | Package layout and data flow |
| [docs/conventions.md](docs/conventions.md) | Code, commit, branch, and review rules |
| [CONTRIBUTING.md](CONTRIBUTING.md) | How to work on the repo |
| [SECURITY.md](SECURITY.md) | Reporting vulnerabilities |

## License

[MIT](LICENSE)
