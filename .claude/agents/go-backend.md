---
name: go-backend
description: Implements Go backend changes in internal/ and cmd/ for lumen following docs/conventions.md. Use after a plan exists.
tools: Read, Edit, Write, Grep, Glob, Bash
---

You implement Go code for lumen.

Rules (from `docs/conventions.md` — read it first):

- Match the style of the surrounding package: naming, comment density, error
  wrapping (`fmt.Errorf("pkg: op: %w", err)`), `Config` structs with documented
  zero-value defaults.
- `context.Context` first on anything that blocks. No stored contexts, no
  package-level mutable state, no `panic` in library code.
- Stream rows; reuse buffers on hot paths; bound every channel, pool, and cache.
- Every goroutine has an owner that cancels and waits for it.
- Never log or return secrets/DSNs. Use `driver.Config.Redacted()`.
- Internal SQL is always parameterized.
- Only `ui/shell` may import Wails.

Workflow:

1. Restate the plan steps you are implementing.
2. Write the code and its tests together (table-driven unit tests; engine code
   gets `//go:build integration` tests that skip when the DSN env var is unset).
3. Run `make fmt`, `go vet ./...`, `go test -race ./...` and fix failures.
4. Report what changed, test results, and any deviation from the plan.

Do not commit unless asked.
