# ubiqo — agent context

Self-hosted context fabric for AI clients: org → project hierarchy, git-backed
semver'd artifacts, scoped memories, activity digests, compiled per-project
instructions — distributed into Claude Code / any MCP client via `ubiqo_*`
tools and session hooks. Single Go binary + PostgreSQL + system git.

## State of the project

- **P0 is implemented and green**: unit + integration + MCP-protocol e2e
  (`-race`), CI passing, live smoke verified. See the last section of
  `docs/reviews/2026-07-13-productization-review.md` for the revised phase
  plan; P1 = fosite OAuth AS (Claude Desktop connectors), proposal inbox +
  push channel, Postgres RLS, memory promotion queue.
- **Decisions are recorded in `docs/adr/0001–0008` and are binding** — read
  them before changing auth, git handling, RBAC, memory, or context
  compilation. Supersede via a new ADR, not a silent edit.
- Background: `docs/architecture/ubiqo-architecture.md` (design),
  `docs/research/omni-gap-analysis.md` (why this exists),
  `docs/reviews/` (8-perspective productization review).

## Commands

```sh
go test ./...                        # fast: unit + compiler goldens
UBIQO_TEST_DATABASE_URL=postgres://ubiqo@localhost:5433/ubiqo_test?sslmode=disable \
  go test ./... -race -count=1      # full: + store/httpapi/MCP e2e (needs PG 16 + git ≥ 2.38)
go test ./internal/compiler -update  # regenerate goldens (deliberate changes only)
go run ./cmd/ubiqo serve --local     # local server (env: UBIQO_DATABASE_URL, UBIQO_DATA_DIR)
go run ./cmd/ubiqo seed              # demo fixture; prints two device tokens
```

Two-account demo: `demo/README.md` (docker compose; nothing else on host).

## Conventions & invariants

- **Git is the source of truth** for artifacts/proposals/releases; Postgres
  rows are rebuildable projections (`ubiqo fsck`). Mutate git first (ADR-0004).
- **All git mutation goes through `internal/gitstore`** (exec system git,
  per-repo lock, path validation). Never add go-git or bypass the wrappers.
- **Every MCP tool declares an authz action** in `internal/mcpserver` and is
  named `ubiqo_*`; authorization happens once, in `internal/core` via
  `authz.Can`. A registry test enforces this — keep it passing.
- **Anything member/agent-authored entering model context goes through
  `compiler.Frame()`** (untrusted-data envelope, ADR-0008). Only
  admin-authored instructions and server-controlled fields may enter the
  instruction layer.
- Compiler output is **golden-tested**; behavior lives in that prose. Review
  golden diffs like code.
- Migrations in `internal/store/migrations/` are forward-only, append new
  numbered files; `events` is append-only (trigger-enforced).
- Denials/errors returned to agents should teach the next step
  (`{reason, caller_role, allowed_alternative}`).
- Tests that need Postgres skip without `UBIQO_TEST_DATABASE_URL`; the DB
  name must end in `_test` (guard in `ResetForTest`).

## Repo layout

`cmd/ubiqo` (server + CLI + hooks, one binary) · `internal/core` (service
façade) · `internal/mcpserver` (tool surface) · `internal/gitstore` (artifact
plane) · `internal/compiler` (context bundles) · `internal/store` (control
plane) · `internal/httpapi` (/mcp + ops endpoints) · `deploy/` (compose/TLS) ·
`demo/` (two-account walkthrough) · `clients/claude-plugin` (hooks plugin;
marketplace manifest at repo root).
