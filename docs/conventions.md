# Code conventions

These rules apply to every change. CI enforces the ones a tool can check;
reviewers enforce the rest.

## 1. Principles

In priority order when they conflict:

1. **Security** — fail closed, never leak secrets, treat every SQL string and
   every remote response as untrusted.
2. **Correctness** — behaviour is proven by tests against real engines.
3. **Performance** — stream, don't buffer; bound every queue, pool, and cache.
4. **Scale** — no global state; every long-lived goroutine has an owner that
   stops it.
5. **Clarity** — small packages with one job; boring code over clever code.

## 2. Layout

- One Go module. Application code lives in `internal/`; nothing is exported
  outside the module.
- `cmd/<binary>/main.go` only parses flags and wires services. No logic.
- `ui/shell` is the **only** package allowed to import Wails. `internal/` never
  imports `ui/`.
- Dependency direction: `app → query → (conn, audit, store) → driver`.
  Lower layers never import higher ones. Cycles are a design bug.
- Each engine lives in `internal/driver/<engine>` and registers itself; core
  packages depend on the `driver` interfaces only.
- Test fixtures go in `testdata/` next to the test or at the repo root.

## 3. Go style

- Format with `gofumpt`; imports grouped std / third-party / local
  (`goimports -local github.com/patrick-rontech/kiemdb`).
- Names: short, lowercase package names (`audit`, not `auditlog` or `utils`).
  No `util`, `common`, `helpers`, `misc` packages.
- Constructors are `New(...)`; options use a `Config` struct with documented
  zero-value defaults, not variadic option funcs, unless there are many
  optional knobs.
- Accept interfaces, return structs. Define interfaces in the consuming
  package and keep them small.
- `context.Context` is the first parameter of anything that does I/O or can
  block. Never store a context in a struct.
- No `init()` except driver registration. No package-level mutable state.
- No `panic` in library code. `must*` helpers only in `main` and tests.
- Comments explain *why*. Every exported identifier has a doc comment that
  starts with its name.

## 4. Errors

- Wrap with context: `fmt.Errorf("audit: append: %w", err)`. Prefix with the
  package name at package boundaries.
- Sentinel errors are `var ErrX = errors.New("pkg: ...")`; check with
  `errors.Is` / `errors.As`, never by string.
- Errors returned to the UI must not contain passwords, DSNs, or row data.
  Use `driver.Config.Redacted()` when logging connection details.
- Handle every error exactly once: either return it or log it, not both.

## 5. Concurrency and performance

- Every goroutine is started by an owner that can stop it (`context` or a
  `Close`) and waits for it (`sync.WaitGroup` / `errgroup`).
- Channels and queues are bounded. Unbounded `append` of result rows is
  forbidden: results are streamed in pages with a max-row cap.
- Reuse buffers on hot paths (row scanning, page encoding). Measure before
  optimizing anything else — add a `Benchmark*` with the change.
- Protect shared maps with `sync.Mutex` / `sync.RWMutex`; keep critical
  sections free of I/O.
- Run tests with `-race`. A data race is a release blocker.

## 6. Security

- **Secrets:** passwords live in the OS keychain (`internal/secret`), never in
  the local SQLite file, logs, audit rows, or error messages.
- **Read-only mode** is enforced twice: by the statement classifier and by the
  engine (session read-only flag / `query_only` pragma).
- **Classifier fails closed:** anything it cannot prove is a read is treated as
  a write.
- **Production connections** require explicit confirmation for writes.
- **One statement per request.** Multi-statement execution is disabled at the
  driver level (no `multiStatements`, extended protocol on PostgreSQL).
- **Audit log** is append-only (DB triggers) and hash-chained; never add an
  update or delete path.
- Use parameterized queries for every internal query. String-building SQL is
  only allowed for the user's own SQL text, which is passed through verbatim.
- New dependencies need a reason in the PR description and must pass
  `govulncheck`.

## 7. Testing

- Table-driven tests with `t.Run`; test names describe behaviour
  (`TestClassify/with_cte_delete_is_write`).
- Engines are tested against real databases (`deploy/compose.yaml`), not mocks.
  Integration tests use the `integration` build tag and skip cleanly when the
  `KIEMDB_TEST_*_DSN` variable is unset.
- Use `t.Context()`, `t.TempDir()`, `t.Cleanup()`; no sleeps for sync.
- New behaviour ships with tests. Bug fixes ship with a test that fails before
  the fix.

## 8. Commits

[Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<scope>): <summary in imperative, ≤ 72 chars, no period>

<body: what and why, wrapped at 72>

<footer: Refs #123, BREAKING CHANGE: ...>
```

- **Types:** `feat`, `fix`, `perf`, `refactor`, `test`, `docs`, `build`, `ci`,
  `chore`, `revert`.
- **Scopes:** package or area — `driver`, `postgres`, `mysql`, `sqlite`,
  `conn`, `query`, `audit`, `store`, `secret`, `app`, `ui`, `ci`, `deps`.
- One logical change per commit. Each commit builds and passes tests.
- Don't mix refactors with behaviour changes.

## 9. Branches and pull requests

- `main` is protected: PR only, linear history (squash or rebase), CI green,
  one approval, conversations resolved.
- Branch names: `<type>/<short-kebab-desc>`, e.g. `feat/mysql-driver`,
  `fix/audit-chain-verify`. Optionally prefix the issue: `feat/42-mysql-driver`.
- Keep PRs under ~400 changed lines where possible; split by package.
- Fill in the PR template, including the security and performance checklist.

## 10. Versioning

Semantic versions. Each passed phase gate is tagged `v0.0.N`; the first public
release is `v0.1.0`. Tags trigger the release workflow.
