# Productization Review — Synthesis of 8 Perspective Reviews

**Date:** 2026-07-13 · **Input:** [`docs/architecture/ubiqo-architecture.md`](../architecture/ubiqo-architecture.md) v0.1 · **Raw reviews:** [`2026-07-13-perspective-reviews.md`](2026-07-13-perspective-reviews.md)

Eight independent reviews (DevOps, sysadmin, security, architect, product, business, UX, QA). **All eight answered "no fundamental architecture change needed."** The five-plane, distribution-side MCP design stands. Everything below is sequencing, hardening, and subsystem substitution — decisions now recorded as ADRs 0001–0008 and folded into the P0 plan.

## The consensus corrections

### 1. P0 was cut wrong — the demo is the spec (product, UX, business)
P0 as designed (hierarchy + artifacts + releases + device auth) demos as "a slower git repo over MCP." The magic — account B's Claude narrating account A's work, nothing re-explained — lived in P1/P2 while the things users *experience first* (hooks, plugin, CLI) sat in P3. **Recut (ADR-0006):** P0 = the 15-minute demo end-to-end: server + device tokens + hierarchy + git-backed `shared/` + releases + **SessionStart hook + activity digest + thin `remember`/`recall`** + solo-mode defaults + local no-TLS eval path. Cut from P0: pgvector/hybrid search, rclone, SeaweedFS, locks, Casbin, console, mem0, full OAuth AS — each behind an interface or tool name so no door closes.

### 2. Two "hidden L" subsystems defused (architect, QA, security)
- **go-git cannot three-way merge** — `merge(id)` as designed was unbuildable. **ADR-0002:** exec the system `git` binary for all mutations (temp local clone → commit/merge/push; `merge-tree` for conflict reports), per-repo write serialization, everything behind an `ArtifactStore` interface. Drops the go-git dependency entirely and gets `gc`/maintenance for free. QA's conflict corpus becomes the acceptance test.
- **OAuth 2.1 AS is not an "M"** — authorize/token/device endpoints, PKCE, DCR (Claude Desktop requires it), refresh rotation, revocation are a project of their own, and security says never hand-roll it. **ADR-0003:** P0 ships opaque device-paired tokens (hashed at rest, admin/self issued, revocable) — sufficient for Claude Code/CLI/hooks; P1 embeds ory/fosite (or delegates) for Desktop connectors. Honest limitation documented: claude.ai Desktop connectors wait for P1.

### 3. Git is the single source of truth (architect)
Proposal = branch, release = tag (+ notes file); Postgres rows for proposals/releases are **rebuildable projections**. Mutate git first; a startup reconciler heals drift (also answers DevOps/sysadmin restore-consistency worry). **ADR-0004.**

### 4. RBAC: hand-rolled matrix, not Casbin (architect, QA)
Two scopes × seven fixed roles = a constant map behind a one-method interface (`authz.Can`), exhaustively table-tested, enforced **deny-by-default in a single tool interceptor** (every registered tool declares its required role; a registry test asserts none forgot). Casbin was a middle stop nobody needed; OpenFGA remains the scale-up path. **ADR-0005.**

### 5. Security hardening that can't be retrofitted (security)
- **Untrusted-data framing now:** activity digests, artifact content, and user-authored strings entering compiled context are wrapped in delimited untrusted-data envelopes and never compiled into the *instruction* layer. The two-hop injection path (poisoned artifact → review → everyone's context) is documented as the maintainer-merge trust boundary.
- `org_id` on every table from migration 0001 (RLS policies land P1, schema shape is the unretrofittable part).
- Token hygiene: hashed at rest, short-lived where possible, revocation + offboarding invalidation, new-device audit events.
- Plugin supply chain: pinned versions + hashes; signing (minisign/Sigstore) before the marketplace is promoted beyond "your own server."
- Git plane: reject symlinks/path traversal in `write_artifact`, no server-side git hooks, size quotas.
- Event log: Postgres grants revoke UPDATE/DELETE; hash-chaining deferred to P1.

### 6. Operations are features (devops, sysadmin)
P0 includes: `/healthz`, `/readyz`, `/metrics` (Prometheus), structured JSON logs; `/v1/meta` version handshake (server ↔ CLI/hook skew); **fail-open hooks** (2s timeout → cached last-good bundle; SessionEnd spooled with retry; visible status line "ubiqo: context v… loaded / OFFLINE — stale"); `ubiqo backup`/`restore` with a cross-store manifest + `fsck` reconciler; single-instance declared as the v0 HA boundary; 12-factor `UBIQO_*` env config with `_FILE` secret variants; forward-only migrations; resource minimums documented. mem0 is **out of the default compose** (P2 opt-in profile).

### 7. UX surfaces specced before they exist (UX, product)
- Onboarding budget: **≤ 10 minutes, ≤ 4 steps** (compose up → `ubiqo setup` → paste MCP snippet → first `get_context`); `ubiqo doctor` validates the deployment.
- **Structured denials:** `{reason, caller_role, allowed_alternative}` — "you're contributor — call `propose_merge`." Permission errors teach the protocol.
- Empty states: fresh projects compile a "getting started" bundle.
- CLI grammar: strict noun-verb (`ubiqo context pull`, `ubiqo memory promote`, `ubiqo device revoke`).
- Approval queues get a push channel (webhook/email) + `ubiqo inbox` in **P1** — digests alone are pull-only and leave proposals waiting.
- Tool names: distinctive, intent-revealing, role + failure mode stated in every description; explicit `project` params.
- Human test scripts (§ UX review) run before v0.1 tag — the only items requiring the product owner personally.

### 8. Strategy (business)
- **License: Apache-2.0** (ADR-0001) + trademark policy + DCO; publish a **free-forever charter** (single-org self-host complete and free; future revenue: managed cloud < $15/seat, multi-org federation, compliance exports, SLA support).
- The clock is the "Anthropic ships it natively" scenario: position as *governed, vendor-neutral context fabric* (self-host, RBAC, versioning, provenance, cross-provider) — not "shared Claude memory." Compress to a demoable wedge in weeks, not months.
- Validate one non-Claude MCP client end-to-end during P0/P1; keep AGENTS.md distribution first-class (the plugin channel is Anthropic-controlled).
- Basic Memory Teams ($15/seat) is the pricing anchor and quarterly-watch competitor, alongside Omni's release feed.

### 9. Test strategy (QA)
Thin MCP adapters over a core service interface; unit-test the core. Golden files pin compiler output (injected clock, sorted collections). Conflict corpus pins the merge engine. Role-by-tool negative matrix pins RBAC. MCP e2e drives the real protocol over in-memory transport using the official SDK client — no Claude needed. The §8 "day at Acme" walkthrough becomes `ubiqo seed`, the canonical fixture for integration/e2e/demo. PR gate < 5 min (unit + goldens + race); Postgres integration gated by env; compose smoke in CI.

## Revised phase plan

| Phase | Scope (delta from original) |
|---|---|
| **P0 — the wedge** | fabric + **distribution-minimal** (hook + snippet + minimal plugin) + **thin memory** + activity digest + solo mode + ops endpoints + backup + seed/demo |
| **P1 — teams** | fosite OAuth AS (Desktop connectors), merge proposals + push-channel inbox, RLS, memory promotion queue, plugin signing, `ubiqo inbox` |
| **P2 — memory depth** | mem0 provider (opt-in profile), extraction candidates, decay/TTL, digests v2 |
| **P3 — polish** | roaming profiles (experimental), console (read-only queues first), marketplace polish, non-Claude client validation hardening |
| **P4 — enterprise** | search substrate (Omni/Onyx), SSO federation, multi-org, compliance exports |

## Decision log

| ADR | Decision |
|---|---|
| [0001](../adr/0001-license-apache-2.md) | Apache-2.0 + DCO + free-forever charter |
| [0002](../adr/0002-artifact-plane-exec-git.md) | Artifact plane executes system git; no go-git; `ArtifactStore` interface; per-repo write lock |
| [0003](../adr/0003-p0-auth-device-tokens.md) | P0 auth = opaque device tokens (hashed); P1 = ory/fosite OAuth 2.1 AS |
| [0004](../adr/0004-git-source-of-truth.md) | Git authoritative for artifacts/proposals/releases; PG rows are projections; reconciler |
| [0005](../adr/0005-handrolled-rbac.md) | Constant role→action matrix + deny-by-default tool interceptor; no Casbin |
| [0006](../adr/0006-p0-recut.md) | P0 scope recut around the 15-minute demo; cut list + what stays behind interfaces |
| [0007](../adr/0007-memory-native-first.md) | P0 memory = native Postgres FTS remember/recall; mem0 as P2 `MemoryProvider` |
| [0008](../adr/0008-untrusted-data-framing.md) | All user/agent-authored content enters context inside untrusted-data envelopes, never the instruction layer |
