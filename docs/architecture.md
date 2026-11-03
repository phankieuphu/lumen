# Architecture

## Packages

| Package | Job | Depends on |
| --- | --- | --- |
| `cmd/lumen` | Parse flags, wire services, start the shell | `app`, `ui/shell` |
| `ui/shell` | The only Wails import; opens the window, serves the embedded UI, binds `app.API`, and emits events | `app` |
| `internal/app` | Façade the UI calls; maps domain types to DTOs. `API` is the UI-facing subset the shell binds, without lifecycle methods | `query`, `conn`, `audit`, `schema`, `store` |
| `internal/query` | Run / cancel by request ID, page streaming, classifier | `conn`, `audit`, `store`, `driver` |
| `internal/conn` | Connection profiles, pool cache, read-only, env tags | `driver`, `store`, `secret` |
| `internal/audit` | Append-only, hash-chained log; verify; CSV/JSON export | `store` |
| `internal/store` | Local SQLite file, embedded numbered migrations | — |
| `internal/secret` | OS keychain (and in-memory store for tests) | — |
| `internal/schema` | Tables, columns, PKs, and foreign keys from catalog queries, bounded by table/column limits | `driver` |
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

`ui/frontend` is React + TypeScript built with Vite. `npm run build` (or
`make ui-build`) writes to `ui/shell/dist`, which `ui/shell` embeds with
`go:embed`. A checkout holds only a placeholder there, so Go builds and tests
work without Node; such a binary shows a "UI not built" page. `make app`
builds both.

Releases: `scripts/package.sh` builds and packs one target (Linux tarball,
universal macOS `.app` zip, Windows zip). CI runs it for all three on every
pull request; `.github/workflows/release.yml` runs it on a `v*` tag and
publishes a GitHub release with `SHA256SUMS`, using
`docs/releases/<tag>.md` as the notes when present.

| Path | Job |
| --- | --- |
| `src/api` | DTO types mirroring `internal/app/dto.go`, the `Backend` interface, Wails bindings, dev mock |
| `src/lib` | Pure helpers: `ResultStore`, grid layout math, formatting, ER graph + dagre layout (`erd.ts`) (unit-tested) |
| `src/hooks` | `useAsync`, `usePaged` (keyset paging), `useQueryRunner` (run / cancel / confirm) |
| `src/components` | `VirtualGrid` and the grids built on it, `SqlEditor` (CodeMirror 6), icons, shared UI |
| `src/screens` | Query editor, history, relations (React Flow, lazy-loaded), slow-query advisor, monitor, connections, audit log, AI settings |
| `src/styles.css` | Theme tokens (`:root` custom properties) and all styling; the editor theme reads the same tokens |

The layout is an icon rail plus one screen. The query editor stays mounted
while hidden, so its text and last result survive switching screens. IBM
Plex Sans and Mono are bundled from `@fontsource` so the app works offline.

The advisor, monitor, and AI settings screens are typed against `Backend`
methods whose Go side is not built yet (phases 3–5). The Wails backend rejects
those calls with a "not available yet" message that the screens show; the dev
mock (`src/api/mockFeatures.ts`) returns sample data. The monitor polls on an
interval owned by an effect, which stops it on pause, on another connection,
and on unmount.

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
