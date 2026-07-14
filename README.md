# ubiqo

**Self-hosted context fabric for AI clients.** ubiqo pushes your team's shared
context — instructions, versioned artifacts, memories, activity — *into* the
AI tools you already use (Claude Code, Claude Desktop, any MCP client). It is
not another chat app: your AI clients stay the interface; ubiqo makes them
share one brain, across accounts and across machines.

Two problems it kills on day one:

- **Two Claude accounts, zero shared awareness.** Both accounts attach to the
  same ubiqo as one identity (or as teammates); each session starts with a
  digest of what the other did, shared memories, and versioned shared files.
- **Two computers, zero continuity.** Context, memory, and artifacts live
  server-side; any machine you sit down at rehydrates the same state.

Apache-2.0 · single Go binary · PostgreSQL + git under the hood · [architecture](docs/architecture/ubiqo-architecture.md) · [ADRs](docs/adr/) · [why not Omni/Basic Memory/mem0](docs/research/omni-gap-analysis.md)

> **Fastest full demo:** [`demo/`](demo/README.md) spins up the fabric plus two
> throwaway "workstations" with Docker — sign into a different claude.ai
> account in each and run the scripted 15-minute walkthrough.

## 15-minute quickstart (local eval, no TLS)

Prereqs: Go 1.24+ (or Docker), PostgreSQL, git ≥ 2.38.

```sh
# 1. server
export UBIQO_DATABASE_URL=postgres://ubiqo@localhost:5432/ubiqo?sslmode=disable
export UBIQO_DATA_DIR=$HOME/.ubiqo-data
go run ./cmd/ubiqo migrate
go run ./cmd/ubiqo seed        # demo org "acme": jon (account A) + sam (account B) — prints both tokens
go run ./cmd/ubiqo serve --local &

# 2. attach Claude Code as sam ("account B")
claude mcp add --transport http ubiqo http://localhost:8383/mcp \
  --header "Authorization: Bearer <sam-token>"

# 3. the aha
#   Ask Claude: "What happened in website-redesign while I was away?"
#   It calls ubiqo_get_context / ubiqo_get_activity and narrates jon's open
#   proposal, the brand-kit v0.1.0 release, and the shared memory that the
#   client prefers Tailwind v4 — none of which you told it.
```

Then review jon's proposal entirely in chat: `ubiqo_list_proposals` (with
diff) → `ubiqo_merge_proposal` → `ubiqo_release_artifact`. Role checks are
real: jon's account *cannot* merge — it gets a structured denial that teaches
the correct next step.

### Production (compose)

```sh
cd deploy && cp .env.example .env   # set POSTGRES_PASSWORD, UBIQO_PUBLIC_URL
docker compose --profile caddy up -d
docker compose exec ubiqo ubiqo setup --org acme --user you --project scratch
```

### Session hooks (context that shows up by itself)

```sh
ubiqo login --server https://ubiqo.example.com --token ubq_...
cd ~/work/my-project && ubiqo init --project my-project
/plugin marketplace add /path/to/this/repo && /plugin install ubiqo@ubiqo
```

Every Claude Code session in that directory now starts with
`ubiqo: context <version> loaded`, the compiled project instructions, team
activity, and memories. Hooks are **fail-open**: server down → visible
OFFLINE line + last cached context, never a blocked session.

## What's inside

| Plane | What it does | Backed by |
|---|---|---|
| Context | org → project → member hierarchy; compiled CLAUDE.md/AGENTS.md per project | context compiler |
| Artifact | shared files, branch-per-user proposals, maintainer merges, **semver releases** (`artifacts/<name>/v1.2.0`) | server-owned git repos |
| Memory | explicit `remember`/`recall`; user-private vs project-shared scopes | Postgres FTS (mem0 provider planned) |
| Activity | append-only event feed → session digests ("what did account A do?") | Postgres |
| Distribution | 12 `ubiqo_*` MCP tools + session hooks + this repo as a plugin marketplace | MCP streamable HTTP |

## Security posture (short version)

- Your data never leaves your infrastructure; ubiqo calls **no** external API.
- Device tokens are hashed at rest, individually revocable (`ubiqo device revoke`), and scoped to a user.
- RBAC (maintainer/contributor/viewer) is enforced server-side on every tool call; denials are structured and auditable.
- Everything member- or agent-authored enters model context inside untrusted-data framing — teammates' files are data, not instructions.
- Events are append-only (trigger-enforced); artifact history is immutable git with provenance trailers (who, which account/machine, which server version).
- Private (`scope=user`) memories are never served to other users — covered by tests.

## vs. the adjacent tools

| | ubiqo | Basic Memory Teams | mem0/OpenMemory | Omni (getomni.co) |
|---|---|---|---|---|
| Self-hosted & free | ✅ Apache-2.0 | ❌ teams tier is paid cloud | ✅ (memory only) | ✅ (search platform) |
| Cross-account/machine | ✅ | ✅ (paid) | single-machine focus | own web UI instead |
| RBAC | ✅ roles + denials | ❌ | ❌ | inherited source ACLs |
| Versioned artifacts | ✅ semver on git | ❌ | ❌ | ❌ |
| Instruction distribution | ✅ compiled per project | ❌ | ❌ | ❌ |

## Free forever

The single-org, self-hosted ubiqo — everything in this repo — is complete and
free, permanently. If ubiqo ever earns money it will be from managed hosting,
multi-org federation, compliance exports, and support — never by fencing off
what you're reading now.

## Roadmap

P0 (this) → P1 OAuth for Claude Desktop connectors (ory/fosite), merge-queue
inbox + push notifications, Postgres RLS → P2 mem0 memory provider +
promotion review → P3 console, roaming profiles → P4 enterprise search
substrate. Details: [productization review](docs/reviews/2026-07-13-productization-review.md).

## Development

```sh
go test ./...                       # unit + goldens (no DB needed)
UBIQO_TEST_DATABASE_URL=postgres://ubiqo@localhost:5433/ubiqo_test?sslmode=disable \
  go test ./... -race -count=1     # + integration + MCP e2e
go test ./internal/compiler -update # regenerate goldens deliberately
```
