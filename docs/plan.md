Oct 30, 2026 · @PHAN KIEU PHU

The build runs in six phases from Nov 2, 2026 to a first public release on Apr 9, 2027: about 23 calendar weeks for one developer part-time, including two buffer weeks for the year-end holidays and Tết.

## Decisions this plan assumes

The design document left five questions open; this plan picks a default for each so work can start, and each one can still change.

| Question | Default in this plan | Why |
| --- | --- | --- |
| Wails v3 beta or v2 | Wails v3, kept behind one thin `ui/shell` package | Its API is stable; the thin package makes a switch to v2 cheap |
| React or Svelte | React with TypeScript | More ready parts for CodeMirror 6 and virtualized grids |
| PostgreSQL parser | `pg_query_go` for Linux and macOS builds; a pure Go parser if Windows CGO builds hurt | Exact PostgreSQL parsing matters for lint and debug |
| HypoPG for index proof | Optional; without it the advice is labelled "not verified" | Many servers do not have the extension |
| Open source | Public repo from day one, MIT license | Portfolio value and early feedback |
| App name | Working name `kiemdb` in code; final name later | SoiDB is taken; the name is a one-line change if chosen before release |

## Milestones

Each phase ends with a gate, and the next phase starts only when the gate passes.

| Phase | Dates | Weeks | Gate |
| --- | --- | --- | --- |
| 1. Core client + audit log | Nov 2 – Dec 4, 2026 | 5 | Connect, run, and cancel a query on PostgreSQL, MySQL, and SQLite; every write appears in the audit log |
| 2. Rule help + Explain + Debug | Dec 7, 2026 – Jan 8, 2027 | 4 + 1 buffer | Daily use without AI: autocomplete, lint, Explain tab, Locks tab, step through a query |
| 3. AI version 1 | Jan 11 – Jan 29 | 3 | Text-to-SQL, error fix, and inline completion pass the AI test set |
| 4. Slow-query advisor | Feb 1 – Feb 26 | 3 + 1 buffer (Tết) | One verified fix on a real database, with cost before and after |
| 5. Monitor + lock tree | Mar 1 – Mar 26 | 4 | Runs 24 hours without leaks; alerts and blocking tree work |
| 6. Polish + first release | Mar 29 – Apr 9 | 2 | Signed installers for Windows, macOS, and Linux; v0.1.0 tagged |

After v0.1.0, phase 6 continues as ongoing work: SSH tunnels, SQL Server, ClickHouse, and AI on monitor data.

## Repository layout

One Go module with the core in `internal/`, so every module can be tested without the UI.

```text
kiemdb/
├─ cmd/kiemdb/            main.go: starts Wails and wires the services
├─ internal/
│  ├─ driver/              Driver, Conn, optional interfaces (Stats, SlowQuery, Lock)
│  │  ├─ postgres/
│  │  ├─ mysql/
│  │  └─ sqlite/
│  ├─ conn/                connection manager, pools, keychain secrets
│  ├─ query/               run, cancel, paging, statement classifier
│  ├─ audit/               append-only log, hash chain, export
│  ├─ sqlparse/            parser adapters per dialect
│  ├─ lint/                lint rules
│  ├─ explain/             plan normalizer, findings, "how it ran" text
│  ├─ debug/               query splitter, script stepper, dry run
│  ├─ locks/               session locks, blocking tree
│  ├─ ai/                  providers, context builder, validator, cache
│  ├─ advisor/             slow-query flow and verification
│  ├─ monitor/             pollers, samples, alerts
│  └─ store/               local SQLite, migrations
├─ ui/
│  ├─ shell/               the only package that imports Wails
│  └─ frontend/            React + TypeScript (editor, grid, tabs, screens)
├─ testdata/               sample schemas, AI test set
├─ deploy/compose.yaml     PostgreSQL and MySQL for tests
└─ .github/workflows/      CI and release
```

## Tasks by phase

Tick tasks off as they are done; each list ends with its phase gate.

### Phase 1 · Core client + audit log

- [ ] Repo, Go module, Wails v3 + React skeleton, CI that builds on three systems
- [ ] `store`: local SQLite file, numbered migrations, `settings` and `app_sessions`
- [ ] `driver` interfaces and PostgreSQL driver (`pgx`)
- [ ] MySQL and SQLite drivers
- [ ] `conn`: connection manager, pools, OS keychain, environment tag, read-only mode
- [ ] `query`: run, cancel by request ID, stream rows in pages as events
- [ ] Statement classifier (read, write, schema) used by read-only mode and audit
- [ ] `audit`: append-only writes from the query path, hash chain, CSV/JSON export
- [ ] UI: connection list and form, editor (CodeMirror 6), virtualized result grid, query history
- [ ] UI: audit log screen
- [ ] Gate: query and cancel on all three databases; writes appear in the audit log

### Phase 2 · Rule help + Explain + Debug

- [ ] Schema cache with version number and refresh after DDL
- [ ] Autocomplete from schema and dialect keywords, under 50 ms
- [ ] Parser adapters for PostgreSQL and MySQL
- [ ] Lint rules from the design document, with tests per rule
- [ ] `explain`: run `EXPLAIN ANALYZE` safely, normalize the plan tree, findings, "how it ran" text
- [ ] Explain tab with plan table, steps, and findings
- [ ] Locks tab: locks this session took
- [ ] `debug`: split a query into parts and count rows; step through a script; dry run with rollback and time limit
- [ ] Debug tab
- [ ] Gate: one week of daily use without AI

### Phase 3 · AI version 1

- [ ] Provider interface; Anthropic, OpenAI-compatible, and Ollama providers
- [ ] Context builder with token budget and table relevance
- [ ] Output validator: parse every SQL answer, retry once
- [ ] AI cache and `ai_requests` log
- [ ] Inline completion with debounce and cancel
- [ ] Text-to-SQL and error fix with diff view
- [ ] AI settings screen with "what is sent" preview and privacy switches
- [ ] AI test set (about 50 cases) and a command to score it
- [ ] Gate: test set passes; no row data sent with default settings

### Phase 4 · Slow-query advisor

- [ ] Top slow queries from `pg_stat_statements` and `performance_schema`
- [ ] Advice task with fixed JSON answer format
- [ ] Verification: `EXPLAIN` on rewrites, HypoPG for indexes, retry up to three ideas
- [ ] Advisor screen: list, plan, advice, cost before and after, apply or skip
- [ ] Applied fixes written to the audit log
- [ ] Gate: one verified fix on a real database

### Phase 5 · Monitor + lock tree

- [ ] Pollers per connection, stopped on close
- [ ] Metric samples, roll-up after 24 hours, delete after 30 days
- [ ] Charts and metric tiles
- [ ] Session list with end session (confirmed, audited)
- [ ] Blocking tree with root blocker
- [ ] Alert rules and desktop notifications, including lock wait
- [ ] Permission check with the exact `GRANT` message
- [ ] Gate: 24-hour run with stable memory

### Phase 6 · Polish + first release

- [ ] Choose the final name and rename
- [ ] Code signing for Windows and macOS, notarization on macOS
- [ ] Installers and auto-update
- [ ] README, screenshots, and a short demo video
- [ ] Gate: v0.1.0 tagged and installers published

## Testing and release

Every module in `internal/` is tested against real databases, not mocks, because the features depend on how each engine behaves.

| Layer | How it is tested | When it runs |
| --- | --- | --- |
| Drivers, query, audit, explain, locks | Integration tests against PostgreSQL and MySQL containers from `deploy/compose.yaml`, plus SQLite files | Every push |
| Lint rules, classifier, debug splitter | Table-driven unit tests with SQL samples | Every push |
| Lock analysis | Tests that open two sessions and create a real block | Every push |
| AI features | AI test set scored against a model | When a prompt or model changes, and before each release |
| UI | Manual check list per screen; Playwright later | Before each release |
| Builds | Wails build for Windows, macOS, Linux | Every push to main |

- Releases use semantic versions and tags; a tag triggers the release workflow.
- Each passed gate gets an early tag: v0.0.1 after phase 1, v0.0.2 after phase 2, and so on, then v0.1.0 after phase 6.
- Code signing needs an Apple developer account and a Windows certificate; order them in phase 5, because approval can take weeks.

## Week 1

The first week ends with an empty app that opens a PostgreSQL connection and runs one query.

1. Create the repo and Go module; add the folders from the layout above.
2. Run `wails3 init` with the React + TypeScript template; move Wails imports into `ui/shell`.
3. Add `deploy/compose.yaml` with PostgreSQL 17 and MySQL 8.4.
4. Write the `driver.Driver` and `driver.Conn` interfaces and a first PostgreSQL driver with `Connect` and `Query`.
5. Bind one Go method to the UI: run SQL, return rows as JSON.
6. Set up GitHub Actions: `go test ./...` with the containers, and a Wails build on three systems.

The design document has the full architecture, interfaces, and feature rules this plan builds.
