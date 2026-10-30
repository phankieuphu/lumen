## What

<!-- One or two sentences. Link the issue: Closes #123 -->

## Why

<!-- The problem, or the docs/plan.md task this ticks. -->

## How

<!-- Key design points. Call out new interfaces, migrations, or dependencies (with the reason). -->

## Testing

- [ ] Unit tests added/updated (`make test`)
- [ ] Integration tests added/updated where engine behaviour changed (`make test-int`)
- [ ] Manually checked: <!-- what, on which engine -->

## Checklist

**Security**
- [ ] No secrets, DSNs, or row data in logs, errors, store, or audit rows
- [ ] Read-only mode and prod confirmation still hold (classifier + engine)
- [ ] Internal SQL is parameterized; user SQL passes through unchanged
- [ ] Audit log stays append-only

**Performance & scale**
- [ ] Results stream in pages; no unbounded buffers, channels, or caches
- [ ] Every new goroutine has an owner that stops and waits for it
- [ ] Hot-path changes include a benchmark

**Housekeeping**
- [ ] Follows [docs/conventions.md](../docs/conventions.md); `make check` passes
- [ ] Conventional Commit messages, one logical change each
- [ ] Docs / `docs/plan.md` checkboxes updated
