# Perspective Reviews — Raw Findings (2026-07-13)

Eight independent reviews of `docs/architecture/ubiqo-architecture.md` v0.1, each produced without sight of the others. Synthesis and decisions: [`2026-07-13-productization-review.md`](2026-07-13-productization-review.md).

---

## DevOps review

- **[blocker]** No observability anywhere in the design — no health/readiness endpoints, metrics, log format, or traces; the EVENT table is product audit, not ops telemetry — add `/healthz` + `/readyz` (checking Postgres, object store, git volume), Prometheus `/metrics`, and structured JSON logs with request/session IDs in P0; wire Compose healthchecks to them; defer tracing.
- **[high]** No build/CI/release pipeline is defined — adopt monorepo + GitHub Actions + goreleaser: one tag builds the multi-arch, static/distroless server image (digest-pinned in the shipped compose file), CLI binaries for all OSes, and the marketplace update, all stamped with one version; CI = `go test` + lint + a Compose smoke test that boots the stack and exercises OAuth device flow + `write_artifact`.
- **[high]** Server/CLI/plugin version skew is unmanaged: the plugin marketplace auto-updates every client org-wide while self-hosted servers upgrade whenever admins get to it — version all three artifacts in lockstep, add a `/v1/meta` handshake (server version, min-supported CLI) that CLI/hooks check before running, and have each deployment serve its own marketplace pinned to its server version rather than one global repo.
- **[high]** State spans three planes (Postgres, bare git repos on a local volume, object store) with independent backups; restore consistency (RELEASE rows vs. git tags) is undefined, and the git volume makes ubiqo-server stateful — explicitly declare single-instance as the v0 HA boundary, add a startup DB↔git-refs reconciler, and write an ordered backup/restore runbook.
- **[medium]** "Embedded migrations" is the entire upgrade story: no rollback policy, no upgrade-path testing, and mem0 owns its own Postgres schema on its own release cadence — embed goose/golang-migrate forward-only with an "upgrade through every minor" rule, CI-test fresh-install and N-1 upgrades against real Postgres, pin mem0 by image digest in a separate schema/database, and cut mem0 from the P0 compose entirely (it's P2 anyway).
- **[medium]** Server deployment config is unspecified — `policy.yaml`/`project.yaml` are product config, not ops config — go 12-factor: `UBIQO_`-prefixed env vars, a `.env.example` in the compose bundle, fail-fast validation at boot, and redacted effective-config logging.
- **[medium]** Deployment secrets are unaddressed (DB/S3 credentials, OAuth/JWT signing keys, SMTP for magic links, mem0's LLM API key); only bundle-side secret filtering is mentioned — support `UBIQO_*_FILE` docker-secrets-style injection, store device tokens hashed at rest, and document signing-key rotation.
- **[medium]** TLS/proxy is one line ("Caddy"): OAuth 2.1 discovery and streamable-HTTP MCP need a canonical public URL, trusted `X-Forwarded-*` handling, and SSE-safe timeouts — require `UBIQO_PUBLIC_URL` at boot, ship a tested Caddyfile in the bundle, and document generic-proxy requirements (`.well-known` passthrough, no response buffering, long idle timeouts).
- **[low]** Bundling SeaweedFS by default is heavy ops surface for the target team — default to the rclone local-filesystem driver on a volume; document external S3 as the production path.

### Top 3 before v0.1
1. Health/readiness endpoints, metrics, and JSON logs — everything else assumes you can see the service.
2. Single-tag release pipeline with lockstep server/CLI/plugin versioning and the `/v1/meta` skew handshake.
3. Migration upgrade-path CI (fresh + N-1) plus the declared single-instance boundary and backup/restore runbook.

### Fundamental architecture change needed?
No. Single Go binary + Postgres + git-on-volume is right-sized for self-hosters and a two-person team; every gap above is operational hardening, not structure. Accept the one structural consequence explicitly: the local git volume pins v0 to single-instance (active/passive at best) — fine if declared, revisit only if scale-out demand materializes.

---

## Sysadmin review

- **[blocker]** Backup spans three unsynchronized stores — §12 says "everything restorable independently," but Postgres release/merge/memory rows reference git tags and object-store blobs; independent restores guarantee dangling references after any real incident. — Ship `ubiqo backup`/`ubiqo restore` in P0: brief write-freeze (maintenance flag), then `pg_dump` + per-project `git bundle` + object sync into one manifest recording repo SHAs, plus `ubiqo fsck` to detect/repair cross-store orphans. Write the restore runbook and prescribe a quarterly drill.
- **[high]** SessionStart/SessionEnd hooks make ubiqo-server a synchronous dependency of every teammate's Claude session; one outage breaks session starts org-wide — this is the 3am page. — Hooks must fail open: 2–3s timeout, fall back to a cached last-good bundle, spool SessionEnd pushes to disk with retry. Document per-dependency behavior (PG down = full outage; mem0 down = memory-less sessions; object store down = private trees only).
- **[high]** mem0 is the lone Python service in a Go stack: separate CVE/upgrade cadence, its own LLM + embedding credentials, large image — it quietly breaks both the single-binary story and airgap. — Drop it from the default compose (memory is P2 anyway); make it an opt-in profile with healthcheck and non-fatal degradation, plus an "explicit `remember` only, no extraction" mode requiring no mem0.
- **[high]** Upgrade/rollback undefined: embedded migrations have no down path, and org-wide plugin auto-update creates client/server version skew — one bad push breaks every member's hooks simultaneously. — Additive-only migrations within a minor; version handshake between CLI/hooks and server with graceful degradation; pinned, staged plugin releases; rollback runbook = previous image + PG restore.
- **[medium]** P0 install friction: plugin/CLI land in P3, so day one is manual OAuth config, public HTTPS URL, hooks JSON, device flow — TLS/callback-URL misconfig is the classic remote-MCP killer. — Ship `ubiqo doctor` (validates external URL, cert, OAuth callback, hook wiring) with P0; target <15 minutes from `docker compose up` to first successful MCP call.
- **[medium]** No observability story despite Postgres queue tables, promotion queues, and an event log. — Add `/healthz` + `/readyz`, Prometheus metrics (queue depth, hook latency, auth failures), structured logs in P0.
- **[medium]** Airgapped viability unaddressed: memory extraction implies LLM/embedding API calls, magic-link auth implies SMTP, Caddy implies ACME. — Document an offline profile: admin invite codes instead of email, internal-CA TLS, memory off or local embeddings, checksummed offline image bundle.
- **[low]** No resource sizing for a ~6-container stack on a small VPS. — Publish minimums (e.g., 2 vCPU / 4 GB for P0 without mem0/SeaweedFS); make SeaweedFS and console opt-in compose profiles.
- **[low]** Ops docs unscoped (G8 is "templates + docs"). — Commit to: quickstart, backup/restore drill, upgrade guide, failure-mode matrix, hardening guide for the internet-facing OAuth endpoint.

### Top 3 before v0.1
1. Orchestrated `ubiqo backup/restore` + `fsck` with a drillable runbook.
2. Fail-open hooks: cached bundle on pull, spooled retry on push.
3. mem0 out of the default stack + client/server version-skew handshake.

### Fundamental architecture change needed?
**No.** Single Go binary + Postgres-centric is the right self-host shape; the gaps are operational hardening — cross-store consistency tooling, fail-open distribution, optionalizing the Python sidecar — all fixable within the current design.

---

## Security review

- **[blocker]** P0 hand-rolls an OAuth 2.1 authorization server (PKCE + device flow) — unrealistic for a 1–2 person team to get right. Embed a proven library/sidecar (ory/fosite, Zitadel) or delegate per the MCP spec; ship short-TTL access tokens, rotating refresh tokens with reuse detection, RFC 7009 revocation, and rate-limited high-entropy device codes.
- **[high]** Device tokens sit plaintext on laptops for the CLI/hooks path; theft means silent impersonation of the human everywhere. Store in the OS keychain, keep access tokens ≤1h with refresh rotation, emit new-device audit events, propagate revocation to live MCP sessions, and invalidate tokens on org-membership removal (offboarding).
- **[high]** Memory is quarantined but artifacts and events are not: SessionStart injects other agents' session summaries unreviewed, and merged artifacts become org-wide context — a two-hop injection path (poisoned artifact → agent-assisted review → everyone's context). Wrap digests and artifact content in delimited untrusted-data framing, never compile user-authored strings (summaries, notes, names) into the instruction layer, and document maintainer merge as the injection boundary.
- **[high]** The plugin marketplace (a git repo) auto-updates hooks that execute commands on every member machine — an org-wide RCE channel; roaming-profile sync of `.claude/` settings likewise propagates executable config between machines. Sign plugin releases (Sigstore/minisign), pin versions + hashes client-side, restrict push access, and exclude or sign hooks/settings in roaming sync.
- **[high]** Tenant isolation rests solely on Casbin checks in the MCP tool layer; one missed filter — especially in hybrid FTS+pgvector `search` — leaks across orgs. Add Postgres RLS keyed on org_id as defense-in-depth, keep mem0 and the object store network-private (server-only), and add cross-tenant leak tests to CI.
- **[medium]** Git plane hardening: reject symlinks and path traversal in `write_artifact` trees, never execute server-side git hooks, enforce per-file/per-repo size quotas, and disable direct human pushes in v0 (server-mediated writes only, tenet 4 notwithstanding).
- **[medium]** Secret hygiene covers only `policy.yaml` key patterns; secrets will land in artifacts, memories, and compiled bundles distributed to every machine and pasted into claude.ai. Run a gitleaks-class scanner on every write and at bundle compilation; block and alert.
- **[medium]** "Append-only" EVENT log is convention, not control — a compromised server rewrites both audit trails (events and git). Revoke UPDATE/DELETE at the Postgres grant level, hash-chain event rows, and periodically anchor signed checkpoints into object-store backups.
- **[medium]** GDPR erasure conflicts with immutable releases, git history, mem0 vectors, and roaming copies. Define deletion now: crypto-shred `age`-encrypted private trees, memory/event delete-or-pseudonymize APIs, a documented admin history-rewrite procedure, and per-org retention in `policy.yaml`.
- **[low]** SSO federation: link identities by (iss, sub), never email; require `email_verified`; disable magic-link for federated domains to block pre-provisioning account takeover; plan SCIM deprovisioning.

### Top 3 before v0.1
1. Swap the bespoke OAuth AS for an embedded library plus a full token lifecycle (blocker).
2. Device-token protection: keychain storage, rotation, revocation propagation, offboarding hook.
3. Untrusted-data framing for artifacts/events entering compiled context — cheap now, unretrofittable later.

### Fundamental architecture change needed?
No. The trust boundaries (service identities, server-mediated git, quarantined memory, org-rooted tenancy) are sound; the gaps are lifecycle and hardening work. The one structural correction: don't own the authorization server — embed or federate it.

---

## Architect review

- **[blocker]** Tenet 3's "git does merge for free" is false with go-git — it supports fast-forward merges only: no three-way content merge, no conflict detection, no `git gc`/repack, slow `Blame`. §5.1 `merge(id)` cannot be built as designed. Decide now: (a) exec the system `git` binary server-side (also solves repo maintenance), or (b) implement tree-level three-way merge via go-git plumbing with whole-file conflict granularity (base/main/proposal blob compare; both-changed → return both versions to the proposing agent, which reconciles whole files well). Wrap either behind an `ArtifactStore` interface and serialize writes per repo — bare repos aren't safe under concurrent mutation.
- **[high]** The OAuth 2.1 authorization server is a hidden L misfiled inside G2 "M". The Go SDK only *verifies* bearer tokens; authorize/token/device endpoints, PKCE, DCR (required by Claude Desktop connectors), refresh, revocation, RFC 8414/9728 metadata are all yours. For P0, ship opaque device-paired tokens (`ubiqo login` prints code → approve → token row) — sufficient for Claude Code/CLI/hooks. Build the real AS on ory/fosite in P1 when Desktop connectors land.
- **[high]** Dual sources of truth: MERGE_PROPOSAL/RELEASE/ARTIFACT rows in Postgres vs branches/tags in git will drift (crash between commit and insert). Make git authoritative — proposal = branch, release = tag + notes file — with PG rows a rebuildable projection; mutate git first, always.
- **[high]** P0 is still oversized. Cut: hybrid search (pgvector silently drags in an embedding pipeline + server-side LLM keys — ship FTS/`git grep` only), the rclone driver (define a 5-method blob interface; ship local-fs + S3 impls; rclone becomes a third impl later), SeaweedFS bundling (any S3 endpoint), locks, and `search` as a tool. Keep hierarchy, tokens, read/write/release, compiled CLAUDE.md. None of these cuts closes a door — each sits behind an interface or a tool name.
- **[medium]** Casbin is overkill for two fixed scopes and seven fixed roles: policy DSL, adapters, and DB sync to express a static matrix. Hand-roll `authz.Can(user, scope, action)` over a constant role→action map (~100 lines, exhaustively table-tested) behind a one-method interface; the doc already names OpenFGA as the real scale-up path, so Casbin is a middle stop nobody needs.
- **[medium]** The compiler bakes identity ("you are jon, role: contributor") into bundles, making output per project×user with invalidation on every membership change. Make compilation a pure function (templates + policy + state) → per-scope bundle, content-hash as the version, cached; inject the identity/role header at fetch time. No template-versioning system in v0 — Go `text/template` in the repo.
- **[medium]** The official MCP Go SDK is young (v1.x, spec still churning: tasks/elicitation/auth). Pin it, keep tool handlers as plain functions behind a thin registry so the SDK stays an adapter, and test over its in-memory transport.
- **[medium]** Docker-free testing: repositories behind interfaces with in-memory fakes; integration via embedded-postgres (real PG binary, no Docker — viable once pgvector is deferred); git layer against `t.TempDir()` bare repos; MCP e2e over in-memory transport; compose e2e in CI only.
- **[low]** `AI_IDENTITY.oauth_token_id` conflates binding with token lifecycle — separate DEVICE_TOKEN table (N per identity) or `ubiqo devices revoke` can't work. Put `org_id` on every table now.

### Top 3 before v0.1
1. Spike and decide the merge strategy (exec-git vs tree-level three-way) behind `ArtifactStore`.
2. Replace the P0 OAuth AS with device-paired opaque tokens; schedule fosite for P1.
3. Make git the single source of artifact truth; apply the P0 cut list (search, rclone, Casbin).

### Fundamental architecture change needed?
No. The plane separation, distribution-side MCP bet, and Postgres control plane are sound; every correction above is a subsystem substitution already isolated by the design's own interface boundaries.

---

## Product review

- **[blocker]** P0's "aha" is missing — as cut (hierarchy, artifacts, releases, device auth), P0 demos as "a slower git repo over MCP." The §8 magic moments (activity narration, "no re-explaining") live in P1/P2. — Pull the activity digest plus thin `remember`/`recall` (mem0, user/project scope, no promotion queue) into P0, and script the literal 15-minute demo: compose up → bind two accounts → account B narrates account A's session.
- **[blocker]** Distribution sequenced last inverts the funnel — hooks/plugin/CLI (G5/G6) sit at P3, but SessionStart injection is *how users experience the product*; without it, onboarding is hand-edited MCP config and pasted instruction blocks, and the walkthrough doesn't run in Claude Code. — Ship a minimal bootstrap plugin + SessionStart hook in P0; defer only marketplace polish and roaming profiles.
- **[high]** Solo user (the founding problem, P1/P2) is under-served day one — compiled etiquette like "propose, don't merge," locks, and maintainer review are team ceremony that punishes a one-person org. — Ship a solo default (owner role, direct-to-main writes, no proposals) with team mode as an explicit upgrade; the 5-person team can wait for P1's RBAC.
- **[high]** Eval friction: requiring HTTPS + a self-run OAuth 2.1 server before first value will kill laptop trials; the gap analysis's "stdio for local dev" suggestion was dropped. — Add `ubiqo serve --local` (localhost/stdio, token auth) as the documented first-run path.
- **[high]** No success metrics anywhere in the design. — Define activation (first cross-account context recall < 15 min from install), retention (week-2 sessions with digest injected), and depth (users with ≥ 2 bound identities; artifacts released per project).
- **[medium]** Differentiation triage: compiler (G3), identity mapping, and memory promotion are the moat; rclone's 70 backends, AGENTS.md, multi-provider clients, and roaming sync are table stakes or deferrable. — v0.1 = S3/local storage only, Claude clients only.
- **[medium]** Tool surface has agent-UX hazards: generic names (`search`, `lock`, `get_context`) collide with other connected MCP servers; `set_active_project` hides state across turns; `review_merge` is a read named as an action; `write_artifact` silently creates branches. — Use distinctive, intent-revealing names, accept explicit `project` params with defaults, cap at ~15 tools with when-to-use descriptions.
- **[medium]** Roaming profile (rclone bisync of Anthropic's local state dirs) is fragile and roadmap-exposed — Anthropic could ship or break it. — Keep it experimental/opt-in; never a marketing pillar.
- **[medium]** README/docs must nail: the category one-liner ("pushes context *into* the clients you already use — not another chat silo"), a copy-paste compose + connector quickstart matching the 15-minute demo, the security posture (what admins can't read), a comparison vs Basic Memory Teams ($15/seat validation)/mem0/Omni, and an `llms.txt` — your buyers' agents read the docs first.
- **[low]** Console deferral is defensible (agent-mediated review is the thesis), but P2's promotion queue needs one read-only proposals/queue page, not a full admin console.

### Top 3 before v0.1
1. Recut P0 around the demo: SessionStart hook + activity digest + thin memory recall.
2. Solo mode with zero-ceremony defaults and a local no-TLS eval path.
3. Rename/tighten the MCP tool surface and write the quickstart README against a 15-minute activation metric.

### Fundamental architecture change needed?
No. The five-plane, steal-boring-infrastructure design is sound and independently validated by the gap analysis; the problems are sequencing and packaging — what ships first, not what gets built.

---

## Business review

- **[blocker]** License (open question 5) gates public release and irreversibly constrains monetization — recommend **Apache-2.0** with a trademark policy + DCO. The moat is integration, templates, and ecosystem, not code secrecy; AGPL would chill the enterprise buyers who fund the future business, while open-core (compliance, federation, managed cloud) remains viable atop Apache. Decide before the first public commit.
- **[high]** "Anthropic ships org-shared memory + cloud sessions" is the kill scenario for the Claude-only early adopter this design courts (Jon's two accounts) — and the gap analysis confirms Anthropic already owns every adjacent primitive. The hedges (self-host sovereignty, cross-vendor, RBAC/versioning/provenance Anthropic won't do cross-client) are real — reposition messaging around a governed, vendor-neutral fabric rather than "shared Claude memory," and land P0–P2 within ~2 quarters before the window closes.
- **[high]** The multi-provider hedge is thin where it matters: hooks, plugin, and roaming profiles are Claude-specific; ChatGPT/Gemini get a bare connector with no session-boundary sync — validate one non-Claude client end-to-end during P0/P1 and document the degraded-mode UX honestly.
- **[high]** The phase plan has T-shirt sizes but no timeline or team assumption; G1 (L) plus five M items is realistically 6–12 months to P3 for 1–2 engineers, colliding with the Anthropic window — compress P0 to a 4–6 week demoable wedge (MCP + git-backed `shared/` + releases) and keep the console (G7) behind the CLI indefinitely.
- **[high]** TCO honesty: Basic Memory Teams costs a 5-person team ~$900/yr; self-hosted ubiqo runs ~$300–500/yr infra plus ops labor (even 2 hrs/mo ≈ $1,800–3,600/yr), so "free" loses on TCO for non-technical teams — sell control, RBAC, and versioning rather than savings, and plan a managed-cloud tier under $15/seat as the real BM-Teams competitor and first revenue line.
- **[medium]** Basic Memory Teams is simultaneously market validation (paying customers exist at $15/seat for a subset — no RBAC, versioning, or self-host) and the fastest mover if it adds those — treat it as the pricing anchor and differentiation checklist, monitored quarterly alongside Omni.
- **[medium]** The marketplace/templates moat is asserted but unresourced (G8: "S, ongoing," no community plan) — ship a public template/skill gallery, contribution guide, and 5–10 exemplar org playbooks at launch, and track external template PRs as the north-star ecosystem KPI; note the plugin channel itself is Anthropic-controlled, so keep AGENTS.md-based distribution first-class.
- **[medium]** The enterprise monetization line is undrawn, and one promise already conflicts with convention: OIDC/SSO federation (the classic paid feature) sits in the free plan — publish a "free forever" charter now: single-org self-host stays complete and free; charge for multi-org federation (open question 4), compliance/audit exports, HA, managed hosting, and SLA support.
- **[low]** MinIO (AGPL) as an alternate bundled default muddies the permissive-stack story — bundle SeaweedFS only; document MinIO as user-supplied.

### Top 3 before v0.1
1. Commit the license (Apache-2.0) and publish the free/paid charter.
2. Date the phases; cut P0 to a 4–6 week wedge.
3. Prove one non-Claude client end-to-end.

### Fundamental architecture change needed?
No. The distribution-side MCP fabric over git + Postgres is the right, defensible shape and doubles as the hedge; the risks are timing, positioning, and go-to-market — not structure.

---

## UX review

- **[blocker]** Onboarding is ~9 steps: deploy server + TLS (+ SMTP for magic links — absent from §12), CLI-create account/org/project/roles, add the private marketplace, install plugin, device-flow code, `ubiqo init` binding, first `get_context`. And the friction-removers (G5 CLI, G6 plugin) ship in P3 — early adopters hand-edit `.mcp.json` and hooks. — Pull a minimal plugin plus a one-liner `ubiqo join <url>` into P0; instrument each step; target under 10 minutes to first context.
- **[blocker]** Approval surfaces are homeless before the console: merge proposals and the memory-promotion queue are discoverable only via SessionStart digests — pull-only. No Slack/email/webhook channel exists in the design, so Jon's 09:05 proposal waits until Sam happens to open a session; the Friday queue-skim has nowhere to live. — Ship one push channel (email or Slack webhook per project) plus `ubiqo inbox` in P1.
- **[high]** Hook failures are silent: if `ubiqo ctx pull` fails (expired token, server down), the session starts looking normal but brainless. — Inject a visible status line every session: "ubiqo: context v2026.07.13-3 loaded" vs "ubiqo OFFLINE — stale context".
- **[high]** The claude.ai Projects flow burdens exactly the non-technical teammate: add connector, OAuth, paste the instructions block — then re-paste on every compiler bump, staleness invisible. — Embed the bundle version in the block so `get_context` detects mismatch and replies "instructions outdated — request a new block"; give admins a one-click onboarding packet.
- **[high]** Permission-denied and merge-conflict experiences in chat are unspecced, yet they are the governance moments ("Jon couldn't merge"). Raw errors make agents retry or misreport. — Denials return structured {reason, caller_role, allowed_alternative} ("you're contributor — call `propose_merge`"); conflict reports as chat-sized per-file diffs.
- **[medium]** CLI grammar is inconsistent — abbreviated `ctx pull` vs bare `init`/`promote`/`sync` vs `devices revoke` — and `promote` is ambiguous (artifact or memory?). No error-message style or empty-state copy is specced. — One noun-verb grammar (`ubiqo context pull`, `ubiqo memory promote`, `ubiqo device revoke`); spec errors and empty states in G5.
- **[medium]** Tool names are agent-facing UX; most read well (`whoami`, `propose_merge`), but `search`, `lock`, `activity`, `get_context` are generic — collision-prone beside other MCP servers — and descriptions omit required roles, inviting doomed calls. — State role and failure behavior in every tool description; test against a multi-server client.
- **[medium]** Empty states are undefined: what does a fresh project's `get_context` return — an empty digest? — Compiler emits a "getting started" bundle (next actions for agent and human) when a project has no artifacts or events.
- **[medium]** Human-test before v0.1, stopwatch running: (1) blank laptop to first `get_context`; (2) non-technical teammate onboards a claude.ai Project from a sent packet, unaided; (3) maintainer reviews and merges entirely in chat, including one conflict; (4) contributor asks Claude to merge — does the denial teach the next step; (5) device token expires mid-week — is it noticed; (6) two agents contend for one lock.

### Top 3 before v0.1
1. Bootstrap in P0 — `ubiqo join` + minimal plugin; onboarding is the adoption funnel.
2. A push notification channel and inbox for both approval queues.
3. Structured denial payloads plus hook status visibility in chat.

### Fundamental architecture change needed?
No. The distribution-side MCP fabric is the right shape; the risks are sequencing (distribution polish belongs in P0, not P3) and unspecced interaction surfaces — both fixable within this architecture.

---

## QA review

- **[blocker]** `merge(id)` rests on go-git, whose three-way merge support is historically incomplete — a bad merge silently corrupts the shared plane, the product's whole point. Spike before building: a conflict corpus (same-line, adjacent-line, edit-vs-delete, rename, binary) as golden integration tests on real bare repos; hide merging behind a `Merger` interface so you can shell out to git CLI if go-git fails the corpus.
- **[high]** The design names zero test seams. Make MCP tools a thin adapter over a core service interface: unit-test the core (hierarchy resolution, semver bump, compiler) directly; e2e over the real protocol by driving an in-process server with the official Go MCP SDK client — no Claude client needed; `mcp-inspector --cli` for smoke; a test-mode token issuer keeps e2e auth non-interactive.
- **[high]** Agent behavior depends on compiled prose, so compiler drift is behavior drift. Pin with golden files: fixture state + `policy.yaml` in, byte-exact CLAUDE.md/AGENTS.md/llms.txt out; inject the clock (bundle version is wall-clock-derived today) and sort member/artifact lists for determinism; review golden diffs in PRs like code. Include the secret-pattern refusal as tests.
- **[high]** RBAC bypass is one forgotten Casbin check away, since enforcement is per-tool. Centralize it in a tool interceptor; add a registry test asserting every registered tool declares a required role (deny-by-default), plus a table-driven role-by-tool negative matrix, including service-identity non-escalation and leakage via `search` and `read_artifact` on others' proposal branches or private trees.
- **[high]** Locks and semver semantics live only in compiled instructions — correctness-by-prompt is untestable in CI. Enforce cheap invariants server-side: `write_artifact` warns/rejects when another user holds an unexpired lock; `release_artifact` validates tag format and monotonicity. Whatever stays prompt-only becomes a periodic eval, never a CI gate.
- **[medium]** Concurrency: inject a fake clock so lock-TTL expiry tests never sleep; test two users writing the same artifact concurrently (both land on their own `u/<user>/` branches), two overlapping proposals merged back-to-back (second returns a conflict report, repo stays clean), double-merge and double-release idempotency; run `go test -race` on every push.
- **[medium]** Embedded migrations need an upgrade-path CI job: seed DB+repos at the last release tag, migrate to HEAD, run the smoke suite. The git layout (`artifacts/<name>/vX.Y.Z` tags, `.locks/`, branch naming) is schema too — version it and cover format changes in the same job.
- **[medium]** Make section 8's "day at Acme" the canonical deterministic seed fixture (fixed IDs, fixed clock, `ubiqo seed`) shared by integration, e2e, migration tests, and demos — the walkthrough is your master e2e script.
- **[low]** CI for two people: PR gate = unit + goldens + race (<5 min) plus testcontainers Postgres/git integration (<10 min); MCP e2e, migration, and merge-corpus jobs nightly and on release branches; skip coverage thresholds.

### Top 3 before v0.1
1. go-git merge spike + conflict corpus, with the `Merger` swap decision made.
2. RBAC interceptor + exhaustive role-by-tool negative matrix.
3. SDK-scripted MCP e2e running the Acme fixture: auth, write, merge, release, read@tag.

### Fundamental architecture change needed?
No. The seams are already right — provider interfaces, Postgres, embedded git, a thin MCP layer — so the design is testable as drawn. The one foundational risk, go-git merge fidelity, is isolatable behind an interface and swappable for the git CLI without redesign.
