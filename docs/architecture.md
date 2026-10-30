# Architecture

## Packages

| Package | Job | Depends on |
| --- | --- | --- |
| `cmd/lumen` | Parse flags, wire services, start the shell | `app`, `ui/shell` |
| `ui/shell` | The only Wails import; binds `app` methods and emits events | `app` |
| `internal/app` | Façade the UI calls; maps domain types to DTOs | `query`, `conn`, `audit`, `store` |
| `internal/query` | Run / cancel by request ID, page streaming, classifier | `conn`, `audit`, `store`, `driver` |
| `internal/conn` | Connection profiles, pool cache, read-only, env tags | `driver`, `store`, `secret` |
| `internal/audit` | Append-only, hash-chained log; verify; CSV/JSON export | `store` |
| `internal/store` | Local SQLite file, embedded numbered migrations | — |
| `internal/secret` | OS keychain (and in-memory store for tests) | — |
| `internal/sqlscan` | Dialect-aware lexer and statement splitter | — |
| `internal/driver` | `Driver`, `Conn`, `Session`, `Rows` interfaces; registry | — |
| `internal/driver/{postgres,mysql,sqlite}` | Engine implementations | `driver` |

Later phases add `sqlparse`, `lint`, `explain`, `debug`, `locks`, `ai`,
`advisor`, and `monitor`, each depending only on `driver` and `store`.

## Query path

```text
UI ──Run(req)──▶ app ──▶ query.Runner
                           │ 1. classify SQL (read / write / schema / session)
                           │ 2. policy: read-only? prod confirmation? single statement?
                           │ 3. conn.Manager.Get(id) → cached driver.Conn (pool)
                           │ 4. Conn.Session() → pinned connection, register cancel
                           │ 5. read → stream Rows in pages → Sink (UI events)
                           │    write → Exec → rows affected
                           │ 6. audit.Append for every non-read (incl. blocked/failed)
                           ▼ 7. history row in store
```

Cancel: `Runner.Cancel(requestID)` cancels the request context **and** sends an
engine-side cancel (`pg_cancel` request, `KILL QUERY <id>`, SQLite interrupt).

## Performance rules

- Pools are opened lazily and cached per connection profile; sizes are bounded.
- Rows are streamed: a page is built in a reused buffer and handed to the sink
  synchronously, so a slow UI applies back-pressure instead of growing memory.
- A max-row cap (default 100 000) stops runaway result sets; the summary says
  the result was truncated.
- The audit chain is appended under one mutex; reads (verify, export) stream.

## Frontend

`ui/frontend` is React + TypeScript built with Vite; the shell embeds `dist/`.

| Path | Job |
| --- | --- |
| `src/api` | DTO types mirroring `internal/app/dto.go`, the `Backend` interface, Wails bindings, dev mock |
| `src/lib` | Pure helpers: `ResultStore`, grid layout math, formatting (unit-tested) |
| `src/hooks` | `useAsync`, `usePaged` (keyset paging), `useQueryRunner` (run / cancel / confirm) |
| `src/components` | `VirtualGrid` and the grids built on it, `SqlEditor` (CodeMirror 6), shared UI |
| `src/screens` | Connections, query, history, audit log |

The UI calls `app.App` methods through `Call.ByName` and receives rows as
`query:page` events. `npm run dev:mock` (or `make ui-dev`) runs it in a
browser on an in-memory backend that is left out of production builds.

Rules the UI follows:

- Rows live in a `ResultStore`, outside React state: a page is appended
  without copying earlier rows, and the grid re-renders at most once per
  animation frame however fast pages arrive.
- Every list is a `VirtualGrid`: only rows in view are in the DOM, so cost
  follows the window height, not the result size. History and audit load
  further pages when the end scrolls into view, capped at 10 000 items.
- Editor text stays in CodeMirror; typing never re-renders React.
- Production writes are confirmed in the UI and re-sent with `confirmWrite`;
  the backend stays the enforcer.

## Security rules

See [conventions.md §6](conventions.md#6-security).
