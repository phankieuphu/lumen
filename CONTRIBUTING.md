# Contributing

## Setup

1. Go 1.26+, Node.js 22+, Docker, and `make`. On Linux also the GTK 4 and
   WebKitGTK 6.0 headers for the desktop shell
   (`sudo apt install libgtk-4-dev libwebkitgtk-6.0-dev` on Ubuntu 24.04+).
2. `make tools` installs `golangci-lint`, `gofumpt`, and `govulncheck`.
3. `make db-up` starts PostgreSQL and MySQL; `make test-int` runs integration
   tests against them.
4. `make app` builds the desktop app into `bin/lumen`; `make ui-dev` runs the
   UI in a browser on sample data.

## Releasing

Write the notes in `docs/releases/vX.Y.Z.md`, merge, then tag `main`:
`git tag vX.Y.Z && git push origin vX.Y.Z`. The release workflow builds
and publishes the packages. Tags below `v1` are marked as pre-releases.

## Workflow

1. Open or pick an issue. Larger changes start with a design note in the issue.
2. Branch from `main`: `feat/<short-desc>` (see
   [conventions §9](docs/conventions.md#9-branches-and-pull-requests)).
3. Commit with Conventional Commits, one logical change per commit.
4. Before pushing: `make check` (fmt, lint, test, vuln).
5. Open a PR with the template filled in. CI must be green.

All rules live in [docs/conventions.md](docs/conventions.md).
