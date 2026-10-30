# lumen

A desktop SQL client for PostgreSQL, MySQL, and SQLite with an audit log, query
explain/debug tools, an AI assistant, a slow-query advisor, and a lock monitor.

- **Backend:** Go 1.26, one module, core in `internal/` (testable without the UI)
- **Shell:** Wails v3, isolated in `ui/shell`
- **Frontend:** React + TypeScript (`ui/frontend`)

## Status

Phase 1 of the [plan](docs/plan.md) is in progress. The backend (drivers,
query runner, read-only mode, audit log) and the UI screens are in place:

- **Connections:** list in the sidebar; editor with engine, environment
  (dev / staging / prod), TLS, and read-only. Passwords go to the OS keychain.
- **Query:** CodeMirror 6 editor (Ctrl/⌘+Enter runs), streaming result grid
  that renders only visible rows, and per-connection history.
- **Production safety:** writes on a prod connection ask for confirmation;
  read-only connections reject writes in lumen and in the database session.
- **Audit log:** filter, hash-chain verify, CSV/JSON export, entry details.

The Wails window is not wired up yet (`ui/shell` still runs headless), so for
now the UI runs in a browser on an in-memory mock backend.

## Requirements

- Go 1.26
- Node.js 22+ (frontend)
- Docker (integration tests only)

## Quick start

Backend:

```sh
make tools      # install golangci-lint, gofumpt, govulncheck
make db-up      # PostgreSQL 17 + MySQL 8.4 in Docker
make test       # unit tests
make test-int   # integration tests against the containers
make lint
```

Frontend (`ui/frontend`):

```sh
make ui-dev     # dev server at http://localhost:5173 on the mock backend
make ui-check   # typecheck, unit tests, production build
```

Both targets run `npm ci` first when needed. Inside `ui/frontend` the same
steps are `npm run dev:mock`, `npm test`, and `npm run build`; the build goes
to `ui/frontend/dist/`, which the shell will embed.

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
