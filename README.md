# lumen

A desktop SQL client for PostgreSQL, MySQL, and SQLite with an audit log, query
explain/debug tools, an AI assistant, a slow-query advisor, and a lock monitor.

- **Backend:** Go 1.26, one module, core in `internal/` (testable without the UI)
- **Shell:** Wails v3, isolated in `ui/shell`
- **Frontend:** React + TypeScript (`ui/frontend`)

![Query screen: SQL editor with a streamed, virtualized result grid](docs/images/query.png)

## Screens

| Screen | What it does |
| --- | --- |
| **Connections** (sidebar) | Saved PostgreSQL / MySQL / SQLite connections, tagged `dev` / `staging` / `prod`; add, edit, test, delete |
| **Query** | CodeMirror SQL editor; run with Ctrl/⌘+Enter, cancel mid-stream; rows arrive in pages into a virtualized grid with a row cap. Writes on `prod` ask for confirmation |
| **History** | Past statements for the selected connection with status, kind, time, and row count; click one to reopen it in the editor |
| **Relations** | Interactive ER diagram (React Flow): tables with PK/FK columns, crow's-foot links between joined columns, auto layout left→right or top→bottom, drag, zoom, minimap. Click a table to highlight its relations and see references; focus on a table and its neighbours; find tables; filter by schema; keys-only view; double-click to query; copy as Mermaid |
| **Audit log** | Append-only, hash-chained log of writes and schema changes; filter by connection and time, verify the chain, export CSV or JSON |

<table>
  <tr>
    <td><img src="docs/images/connection.png" alt="New connection form" /></td>
    <td><img src="docs/images/history.png" alt="Query history" /></td>
  </tr>
  <tr>
    <td align="center">Connection form</td>
    <td align="center">History</td>
  </tr>
  <tr>
    <td colspan="2"><img src="docs/images/relations.png" alt="Relations: ER diagram of tables and foreign keys" /></td>
  </tr>
  <tr>
    <td colspan="2" align="center">Relations</td>
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
