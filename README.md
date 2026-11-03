# lumen

A desktop SQL client for PostgreSQL, MySQL, and SQLite with an audit log, query
explain/debug tools, an AI assistant, a slow-query advisor, and a lock monitor.

- **Backend:** Go 1.26, one module, core in `internal/` (testable without the UI)
- **Shell:** Wails v3, isolated in `ui/shell`
- **Frontend:** React + TypeScript (`ui/frontend`)

![Query editor: connections sidebar, SQL editor, and a streamed, virtualized result grid](docs/images/query.png)

## Screens

A dark UI in IBM Plex with an icon rail on the left for switching screens.

| Screen | What it does |
| --- | --- |
| **Query editor** | Connections sidebar; CodeMirror SQL editor; run with Ctrl/⌘+Enter and cancel mid-stream. Rows arrive in pages into a virtualized grid with a row cap. Production connections are marked red, and writes on them ask for confirmation |
| **History** | Past statements for the selected connection with status, kind, time, and row count; click one to reopen it in the editor |
| **Connections** | Saved PostgreSQL / MySQL / SQLite connections tagged Dev / Staging / Production; add, edit, test, delete. Passwords live in the OS keychain |
| **Audit log** | Append-only, hash-chained log of writes and schema changes; filter by connection and time, inspect an entry, verify the chain, export CSV or JSON |

<table>
  <tr>
    <td><img src="docs/images/connection.png" alt="Connection editor" /></td>
    <td><img src="docs/images/history.png" alt="Query history" /></td>
  </tr>
  <tr>
    <td align="center">Connections</td>
    <td align="center">History</td>
  </tr>
  <tr>
    <td colspan="2"><img src="docs/images/audit.png" alt="Audit log with chain verification and export" /></td>
  </tr>
  <tr>
    <td colspan="2" align="center">Audit log</td>
  </tr>
</table>

Screenshots are taken from `make ui-dev`, which runs the frontend in a browser
against an in-memory mock backend.

## Quick start

```sh
make tools      # install golangci-lint, gofumpt, govulncheck
make db-up      # PostgreSQL 17 + MySQL 8.4 in Docker
make test       # unit tests
make test-int   # integration tests against the containers
make lint
make ui-dev     # frontend on a mock backend at http://localhost:5173 (no Go needed)
make ui-check   # frontend typecheck, tests, and production build
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
