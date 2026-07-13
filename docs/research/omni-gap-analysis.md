# Omni (getomni.co) vs. the Ubiqo Vision — Deep Research & Gap Analysis

**Date:** 2026-07-13
**Question:** Does Omni ([github.com/getomnico/omni](https://github.com/getomnico/omni) · [getomni.co](https://getomni.co/)) meet — or expand upon — the intended use case: a free, self-hostable platform for centralizing agentic context (memories, projects, skills, CLAUDE.md instructions, artifacts) shared across multiple Claude/AI-provider subscriptions and machines, with an org → project → user hierarchy, RBAC, semantic versioning, and instruction-driven collaboration?

**Method:** Primary sources read directly (Omni's GitHub repo, docs.getomni.co including its `llms.txt` doc index, release notes v0.1.1→v0.1.10, configuration reference, OpenAPI page), plus a 101-agent research workflow: 5 search angles → 15+ sources fetched → 25 top claims put through 3-vote adversarial verification (19 claims confirmed, 17 of them unanimously, several verified at code level against Omni's actual DB migrations) → synthesis. Product identity was verified for every source — this report is about **getomnico/omni**, not omni.co (BI), Sidero Omni, OmniAI, or omnigent.ai.

---

## TL;DR — Verdict

**Omni is not your product. It's an adjacent one — and a useful quarry.**

Omni is a young (first release March 2026, v0.1.10 July 2026), healthy, Apache-2.0, genuinely self-hostable **open-source Glean alternative**: enterprise search + workplace AI agents over data synced *from* SaaS apps (Drive, Slack, Jira, …), with its own ChatGPT-like web UI. The founder's own Show HN framing: *"Open-source workplace search and chat, built on Postgres."*

Against your eight requirements it **fully meets one** (free/OSS/self-hostable), **partially meets three** (storage backends, RBAC, cross-device continuity — for *its own* UI), and **misses four**. The four it misses — the org→project→user folder hierarchy engine, semantic versioning of shared artifacts, CLAUDE.md/skills orchestration, and any integration into Claude clients themselves — are precisely the core of what you want to build. This was verified down to the schema: an enumeration of all ~104 of Omni's database migrations found **no org/project/folder/team/share tables at all**.

The deepest mismatch is directional. Omni **pulls** company data *into* its own indexed silo and makes you chat *in its UI*. Your vision **pushes** shared context, memory, and instructions *out into* the AI clients people already use (Claude Desktop, Claude Code, Cowork), keeping those clients as the interface. Omni consumes MCP servers as an MCP *client*; it does not expose a documented MCP *server*, so a Claude Desktop instance cannot plug into an Omni deployment as a shared brain today.

That said, Omni independently validates several architecture bets you should copy (identity decoupled from AI-provider accounts, Postgres+pgvector single-store ops, permission inheritance, mem0 as a pluggable memory provider, S3 content-addressed blob storage), and it could serve as a *search/RAG substrate component* behind your platform later. **Nothing surveyed in the broader landscape fills your niche either** — the pieces exist (MCP, git, memory APIs, Anthropic's own team primitives), but nobody has assembled the fabric. The closest partial fits: Basic Memory (free local memory core; its team/sync answer is a paid cloud tier), Mem0 self-hosted (OpenMemory's designated successor), and Omnigent (cross-device *session* continuity for coding agents).

---

## 1. Identity check — which "Omni" is this?

At least five unrelated products share the name. This report is about **getomnico/omni** (getomni.co), "Open-source AI agents for work." It is **not**:

- **omni.co / Omni Analytics** — closed-source business intelligence.
- **Sidero Omni** — Talos Linux/Kubernetes management (its "RBAC" docs pollute search results for "Omni RBAC" — verified different product).
- **OmniAI** — document-extraction API.
- **Omnigent (omnigent.ai)** — a *different* open-source agent orchestration product, covered in §5 (verified zero cross-references between the two repos).

Every claim below was checked against getomnico-specific fingerprints (service names `omni-searcher`/`omni-indexer`/`omni-connector-manager`, ParadeDB, docs.getomni.co).

**Maturity snapshot (2026-07-13):** ~740 GitHub stars, 41 forks, 895 commits, 11 releases from v0.1.1 (2026-03-17) to v0.1.10 (2026-07-07), verified at HEAD `07bc56b`. A ~4-month-old, fast-moving, pre-1.0 project under an unmodified Apache-2.0 license (no dual licensing, no open-core split, no license keys — repo-wide search found zero "proprietary"/"Enterprise License" hits). A commercial "Omni Cloud" hosted service exists in early access, but it's a hosting offering, not a differently-licensed component. Release cadence ~every 10–14 days. Its public OpenAPI reference page is still the docs-template "Plant Store" placeholder — a telling maturity signal for API-first consumers.

## 2. What Omni actually is

**Positioning:** "an AI Agent Platform for the Workplace" / a "unified context layer over workplace knowledge" — employees "find answers, understand context, and get tasks done with agents grounded in the data they already have access to."

**Architecture** (from docs.getomni.co/architecture, confirmed against the repo):

- **Single data layer:** "Instead of stitching together separate systems for storage, search, and message queuing, Omni uses PostgreSQL for all three." ParadeDB (pg_search/Tantivy) provides BM25 full-text; pgvector provides semantic search; a row-locked Postgres table is the message queue. Targets "up to ~5 million documents."
- **Services:** `omni-web` (SvelteKit UI/auth/gateway), `omni-searcher` (Rust, hybrid query + permission filtering), `omni-indexer` (Rust), `omni-docling` (Python, PDF/Office extraction), `omni-ai` (Python/FastAPI LLM orchestration), `omni-sandbox` (Rust, isolated bash/Python/file execution with Landlock), `omni-connector-manager` (Rust) + per-source connector containers.
- **Connectors:** ~23 named integrations / 17 connector packages — Google Workspace (Drive/Gmail/Chat), Microsoft 365 (SharePoint/OneDrive/Outlook/Teams), Slack, Confluence/Jira, Notion, GitHub, Linear, ClickUp, HubSpot, Nextcloud, Paperless-ngx, IMAP, Fireflies, Google Ads, Darwinbox, local filesystem, web crawler. Connector SDKs in Rust, Python, TypeScript.
- **Storage:** `STORAGE_BACKEND=postgres` (default) or `s3` (`S3_BUCKET`, `S3_REGION`, IAM or explicit keys) — content-addressed blob storage (since v0.1.3) with garbage collection. No GCS or Azure Blob backend documented.
- **LLMs:** Bring-your-own — Anthropic, OpenAI, Gemini, Bedrock, Vertex, Azure AI Foundry, any OpenAI-compatible endpoint; plus a llama.cpp local-inference Docker overlay for fully local LLM/embeddings.
- **Deployment:** Docker Compose bundle shipped as a release asset (no license key or vendor account required — verified); Terraform for AWS (documented) and GCP (present in-repo, guide missing).

**Features relevant to your requirements:**

- **Permission inheritance (its flagship security idea):** each document carries a permissions record synced from the source system; enforcement is at query time. Code level: `documents.permissions` JSONB (migration 003), `generate_permission_filter()` in `shared/src/db/repositories/document.rs:660`, ACL tokens like `public:true` / `users:alice@example.com` / `groups:slack-channel:T0:C1` (migration 095). Groups are synced *from* sources (migration 068), not natively administered.
- **Roles:** exactly three, fixed, instance-global — `users.role CHECK ('admin','user','viewer')` (migration 001). Viewer = search/chat only; User = + manage own sources; Admin = everything. Google OAuth built in; Okta/Microsoft Entra SSO optional. Per-user API keys with three scopes (Public/User/Admin), capped at 25, encrypted at rest. Native file uploads are single-user-owned with **no sharing mechanism** (migration 082).
- **Memory:** opt-in and lightly advertised (`MEMORY_ENABLED=true`), pluggable `MemoryProvider` interface with **mem0 as the first implementation** (v0.1.5), per-agent namespaces, API 404s when disabled. "Completed chat turns can be summarized into memories and recalled in later chats." Strictly **per-user** — view/delete only, no manual authoring, no shared/org/project memory pool.
- **Background agents (v0.1.2+):** cron-scheduled agents defined by name + flat prompt + schedule + model. Two scopes: **User agents** and **Org agents** (`agent_type IN ('user','org')`, migration 065) — org agents get admin-whitelisted actions (send email, people search). Tools: search/read documents, read/write files in a sandbox workspace, bash/Python, web search/fetch, connector actions. "There is no per-tool approval prompt," and runs execute with the *creator's* permissions even when other users chat with the agent (a confused-deputy wart worth avoiding in your design). Agents can keep persistent memory across runs.
- **MCP — client side:** v0.1.2 added MCP support for connector developers; v0.1.4 added **remote MCP servers with per-user MCP tool auth**; v0.1.10 added Google/ClickUp remote-MCP OAuth actions and "MCP resources and prompts as agent tools." All verified usages are Omni *consuming* MCP servers inside its own agent loop. The marketing phrase "Model Context Protocol for seamless tool integration" is ambiguous, but **no MCP-server component exists in the repo tree and no doc describes external AI clients connecting to Omni via MCP**. (Watch item: this project moves fast; an exposed MCP server would materially change the calculus — see §8.)
- **Artifacts:** the sandbox has a `present_artifact` tool ("Present a generated file (chart, processed spreadsheet, etc.)") and the web app serves `/chat/{chat_id}/artifacts/{path}` downloads from an S3 bucket — **chat-scoped, ephemeral agent outputs** that don't feed the index. Not authored, versioned, or shared knowledge content.
- **Fun fact:** the repo contains `.claude/skills/build-connector` — the Omni team dogfoods Claude Code skills to develop connectors, but nothing in the product distributes skills/instructions to anyone.

## 3. Requirement-by-requirement gap analysis

| # | Your requirement | Omni today (verified) | Score |
|---|---|---|---|
| 1 | Self-hostable, free, open source | Unmodified Apache-2.0; compose bundle as release asset; AWS/GCP Terraform; llama.cpp overlay for fully-local inference | ✅ **Meets** (15-0 verification) |
| 2 | Pluggable cloud storage for artifacts/files/memories | `STORAGE_BACKEND=postgres\|s3` for *its own* indexed content + chat artifacts. No GCS/Azure; not a user-facing artifact store you organize; memories live in the mem0 provider, not your object store | 🟡 **Partial** |
| 3 | Org → project → user-individual/shared folder hierarchy engine | Nothing. No org/project/folder/team/collection/share tables anywhere in ~104 migrations. Flat single org of users + data sources | ❌ **Misses** (12-0) |
| 4 | RBAC for resource access | Mirrored source-system ACLs (done well) + three fixed instance roles. No custom roles, no in-app resource grants, no multi-tenancy; native uploads unshareable | 🟡 **Partial** (generous) |
| 5 | Semantic versioning of collaborative artifacts | No versioning of anything. Index content is connector-synced snapshots; "artifacts" are ephemeral per-chat sandbox outputs | ❌ **Misses** |
| 6 | CLAUDE.md/skills placed per project/org to orchestrate collaboration | Nearest analog is per-agent flat system prompts + org-agent action whitelists. No instruction hierarchy, no skills system, no distribution to clients | ❌ **Misses** |
| 7 | Tie together multiple Claude accounts' work/memory/context | Not by design. Own identity layer (the right *pattern*) means any authorized human can query one shared instance — but context lives in Omni's UI, memory is per-user, and no MCP server means Claude clients can't attach | ❌ **Misses** (indirect partial credit at best) |
| 8 | Cross-device continuity (Cowork stores locally) | Omni's chats/memory are server-side → continuity *within Omni's own web UI*. Does nothing for Claude/Cowork local state | 🟡 **Partial** for itself, ❌ for Claude clients |

**Scorecard: 1 meets, 3 partial, 4 miss — and the four misses are your product's core.**

One-sentence framing: **Omni is an *ingestion-side* unified context layer (pull company data in; search and act on it inside Omni); Ubiqo is a *distribution-side* context fabric (push shared context, memory, skills, and policy out into every AI client a team uses).** Complementary shapes, not competitors.

## 4. What Omni gets right — patterns worth stealing

1. **Own identity, decoupled from AI-provider accounts.** Omni users authenticate to *Omni* (OAuth/SSO); the LLM vendor behind it is invisible. This is exactly how to solve your two-accounts problem: **ubiqo users are first-class; Claude accounts/machines are just clients of a ubiqo identity.** Don't bridge Anthropic accounts — abstract over them.
2. **Postgres-only operational story.** ParadeDB + pgvector + row-locked queue tables = hybrid search, storage, and messaging in one database you already know how to back up. For self-hosted software aimed at small teams, minimizing moving parts is the whole game.
3. **Pluggable memory provider, mem0 first.** Omni's `MemoryProvider` interface is precisely the abstraction you want — it validates mem0 as a default while keeping Letta/Zep/etc. swappable.
4. **Permission metadata on content, filtered at query time** (`public:` / `users:` / `groups:` tokens) — a clean enforcement kernel you can extend into real RBAC.
5. **Content-addressed S3 blobs + Postgres metadata, with GC** — the correct split for your artifact store.
6. **Org vs. user agent scoping with admin whitelists** — a primitive worth generalizing into your per-project shared-vs-individual split. (But fix the confused-deputy problem: don't run shared agents on the *creator's* credentials.)
7. **`llms.txt` on the docs site** — agent-readable documentation index. Ship one from day one; your product's consumers are literally agents.

**Reuse option:** run Omni as a *component* — the org-knowledge search substrate — behind your platform, fronted by your own MCP server (Omni's REST API + API keys are wrappable). **Fork-and-extend is not recommended:** you'd be bolting a hierarchy engine, versioning, instruction distribution, and an MCP server onto a fast-moving pre-1.0 codebase whose center of gravity (connector indexing + its own chat UI) isn't yours.

## 5. The broader landscape — verified alternatives and complements

No surveyed product delivers all eight requirements. The closest pieces:

### Basic Memory (basicmachines-co/basic-memory) — closest free memory core
- **AGPL-3.0** (LICENSE file verified), and the free local version is the *complete* product: MCP server + CLI + plain-Markdown knowledge files on disk + local SQLite index (Postgres optional). "Local-first. Plain text on your disk. Forever." / "MCP-native. Works with every major AI client and IDE" — compatibility table includes Claude Desktop, Claude Code, Codex, Cursor, VS Code. Actively maintained (v0.22.1, 2026-06-13).
- **The catch, verified 3-0:** its answers to your requirements 7–8 are **paid cloud features** — "Basic Memory Teams" ("Give your team a single, shared cloud workspace… anything a teammate writes is immediately available to everyone else and to their AI assistants") and rclone-powered bidirectional cross-device sync are part of the hosted tier ($15/seat/mo beta, Business $30/seat). **The vendor's own comparison table lists "Manual (Git, Syncthing, etc.)" as the free path** — no self-hostable Teams stack exists. ChatGPT integration also requires the paid cloud (remote-only connections).
- **Read on it:** Markdown-files-as-memory + MCP is the right *format* bet, and its paid Teams tier is direct market validation of your thesis — someone is charging $15/seat for a slice of what you're proposing, without self-hosting, RBAC, or versioning.

### Mem0 / OpenMemory MCP — cross-app memory, one machine; successor is Mem0 self-hosted
- OpenMemory MCP (by Mem0) proved the cross-*application* half of your requirement 7: "Store context in Cursor and retrieve it later in Claude or Windsurf without repeating yourself" — a shared local MCP server (Docker: Postgres + Qdrant, per-client SSE endpoints, dashboard).
- **Being sunset (verified in the repo README):** "OpenMemory is being sunset. For local self-hosted memory with a dashboard, please use the Mem0 self-hosted server instead." Do not build on OpenMemory; **Mem0 self-hosted is the designated successor** (Apache-2.0, also the engine inside Omni's memory feature — two independent products converging on it).
- Verifiers also established the "single machine" limit is default framing, not architecture: a network-exposed self-hosted Mem0 server can serve multiple machines and accounts. That is essentially your requirement-7/8 memory plane, minus hierarchy/RBAC/versioning.

### Omnigent (omnigent-ai/omnigent, omnigent.ai) — cross-device *session* continuity
- A completely distinct product from getomni.co (verified: zero cross-references). Apache-2.0 "open-source meta-harness that gives you a common orchestration layer over Claude Code, Codex, Cursor, OpenCode, Hermes, Pi…" — ~7.2k stars, v0.5.1 (2026-07-10), self-labeled **alpha**.
- Directly relevant to your requirement 8: "Sessions follow you: start in your terminal, continue in the browser, pick it up on your phone. Messages, sub-agents, terminals, and files stay in sync" — via its own self-hostable server layer (localhost, or Kubernetes/Modal/Daytona/E2B/CoreWeave). Continuity of *its* session layer, not of Claude Cowork's local state — the same strategy you should adopt (own the session plane rather than syncing Anthropic's local files).

### Memory-layer honorable mentions (directional; not adversarially verified in this pass)
- **Letta (MemGPT lineage):** fully open-source, self-hostable server, Docker deploy, **ships an MCP server**; memory-as-OS paging model; strongest LoCoMo scores among classic OSS tools. The heaviest but most "agent-native" option.
- **Zep / Graphiti:** the temporal-knowledge-graph engine **Graphiti is open source**, but the full Zep platform has drifted SaaS-first — for self-hosting, Graphiti is the usable piece.
- **Cognee:** OSS memory/GraphRAG pipeline; more a framework than a multi-user service.
- **Onyx (ex-Danswer):** the other big OSS enterprise-search+assistant player — same category as Omni, same mismatch for your use case.

### Anthropic-native primitives (know what you're building *around*)
- **Claude Team/Enterprise memory (Sept 2025 →):** memory is **project-scoped** ("Claude creates a separate memory for each project") but **per-user** — teammates do not share a memory pool. Memories are exportable/portable. Incognito chats exist. **The org-shared memory pool you want does not exist in Anthropic's product line** — that's your gap, confirmed.
- **Claude Projects** (Team/Enterprise): shareable project knowledge + project instructions in the Claude app — the natural 1:1 anchor for your "project" concept, but cloud-only, one org, no versioning, no RBAC beyond membership, nothing for Claude Code/Cowork local state.
- **Claude Code's context hierarchy:** managed/enterprise policy settings → user `~/.claude/CLAUDE.md` → project `CLAUDE.md` → subdirectory `CLAUDE.md`; plus skills directories, hooks, and `.mcp.json` project config. **This layered-instructions model is the exact orchestration mechanism your requirement 6 describes — it already exists client-side; what's missing is the server that generates, versions, and distributes those files per org/project/user.**
- **Claude Code plugins + private marketplaces:** a plugin (installable from a private git repo marketplace) can bundle skills, hooks, MCP server config, and commands, and be updated org-wide. **This is the idiomatic distribution channel for your instruction/skill layer** — likely the single most useful Anthropic feature you haven't mentioned.
- **Remote MCP connectors:** Claude Desktop/Cowork/Code all attach to remote MCP servers with OAuth — the sanctioned way for *any* Claude account, on *any* machine, to reach shared infrastructure. ChatGPT and Gemini surfaces increasingly speak MCP too, which is what makes your "other AI provider" requirement realistic.
- **Claude Code on the web / remote sessions:** server-side execution environments where session state lives in the cloud — Anthropic's own partial answer to cross-device continuity, worth positioning alongside (not against).

## 6. The gap nobody fills — your differentiation

Across everything surveyed, each candidate solves one plane and ignores the rest:

| Plane | Solved today by | Missing everywhere |
|---|---|---|
| Knowledge search over org data | Omni, Onyx (self-hosted) | — |
| Agent memory (single user/app) | Mem0, Basic Memory, Letta | Org/project/user **scoped, shared** memory with promotion rules |
| Cross-app memory (one machine) | Mem0 (ex-OpenMemory) | Cross-**machine**, cross-**account**, free |
| Team shared knowledge for AI clients | Basic Memory Teams (**paid cloud**) | Self-hosted, RBAC'd, versioned |
| Session continuity across devices | Omnigent (alpha), Claude web sessions | For Cowork/Desktop local state; account-agnostic |
| Instruction/skill distribution | Claude Code plugins/marketplaces (client-side mechanism) | The **server** that generates/versions/distributes per org/project/user |
| Artifact versioning & collaboration | git (generic) | Agent-aware semver + provenance + RBAC integrated with the above |

**No one has built the org → project → user context fabric with RBAC, versioning, and instruction orchestration that treats AI clients as the interface.** That's ubiqo's slot. The adversarially-verified conclusion of the research run says the same thing: assemble the hub from a memory layer + a git-backed store — because nothing ships it integrated.

## 7. Improvement ideas you haven't (explicitly) considered

1. **Be an MCP server first, a folder engine second.** The single highest-leverage decision: expose the entire fabric as a **remote MCP server with OAuth** (plus stdio for local dev). That's what makes it work across two Claude accounts, N machines, Claude Desktop/Code/Cowork, *and* non-Anthropic clients — with zero client-side installs beyond adding a connector. Your problem statements 1 and 2 both collapse to "both instances attach to the same remote MCP server as different ubiqo identities."
2. **Use git as the versioning engine instead of inventing one.** Back each project's `shared/` tree with a git repo: semver = tags, individual work = branches or `users/<name>/` trees, collaboration = merge/PR protocol, CODEOWNERS ≈ per-path RBAC hooks, signed commits = provenance. Claude Code already speaks git natively, and Basic Memory's own docs point free users to "Git, Syncthing" for sync — the ecosystem keeps converging here. Semver-tag *releases* of shared artifacts give agents a stable "known-good" pointer while drafts churn.
3. **Distribute instructions via a private Claude Code plugin marketplace.** A tiny `ubiqo` bootstrap plugin (hosted in your own git repo) can install: the MCP connector, hooks, base skills, and a CLAUDE.md include. Updating the plugin updates every member's client. This is the Anthropic-idiomatic answer to your requirement 6 — the server generates per-org/per-project instruction bundles; the plugin channel delivers them.
4. **Hooks as the invisible sync mechanism.** SessionStart hook: pull fresh org/project context + memory digest into the session. SessionEnd/PostToolUse hooks: push progress events, artifact changes, and memory candidates back. Nobody has to "remember to sync" — the client does it on every session boundary. This also directly attacks the Cowork continuity problem: an opt-in "roaming profile" that syncs session state/todos/memory through your storage backend.
5. **Memory needs a promotion workflow, not just storage.** Private-by-default memories; promotion to project/org scope is a *reviewed* act (a PR for memories). Add TTL/decay, per-scope namespaces (validated by both Omni's per-agent namespaces and Anthropic's project-scoped memory), and an incognito escape hatch. Shared memory without curation becomes shared noise.
6. **Progressive disclosure beats bulk context.** Don't dump the org brain into every session. Generate an `llms.txt`-style index + skill-like "load on demand" pointers at each hierarchy level, so agents navigate org → project → folder cheaply and fetch detail only when needed. (This is the skills design pattern applied to your whole fabric.)
7. **Identity mapping table, not account bridging.** `ubiqo_user ↔ {claude_account_a, claude_account_b, machine_1, machine_2, openai_account…}`. Each client session authenticates to ubiqo (OAuth device flow), and everything it writes is attributed to the ubiqo identity — solving "two accounts, one human" cleanly and auditable.
8. **Provenance and audit as first-class metadata.** Record which ubiqo user + which client/account/model/session produced every artifact version and memory (commit trailers do this for free in the git plane). When an agent writes something wrong into shared context, you need to trace and revert it — this is the trust foundation for multi-agent collaboration, and no surveyed product has it.
9. **Concurrency protocol in the instructions themselves.** Advisory locks ("claim `\.locks/<artifact>` before editing"), or branch-per-editor with merge — encoded in the org-level CLAUDE.md so *agents* follow the collaboration protocol without bespoke tooling. Text artifacts merge fine via git; reach for CRDTs only if you later want real-time co-editing.
10. **Avoid the confused deputy.** Omni runs shared agents with the *creator's* permissions; Anthropic's product answer (per-user memory, project boundaries as "safety guardrails") shows the bar. Shared/org agents in ubiqo should run with their *own* scoped service identity, never a member's.
11. **An activity feed is half of "awareness."** Your problem statement is partly a *presence* problem: an append-only event stream per project ("account B's agent updated `shared/api-design/` to v0.3.0, summary: …") that hooks inject as a digest at session start. Cheap to build, huge perceived value.
12. **Storage pluggability via rclone (or an S3-compatible gateway).** Rather than hand-writing S3+GCS+Azure+Dropbox adapters, one rclone-backed driver gives you ~70 backends for artifact/file storage day one (Basic Memory's paid sync is literally rclone-powered). Keep metadata/index in Postgres like Omni does.
13. **Cost/token governance per scope.** Once multiple subscriptions share context, orgs will want per-project usage visibility and budget rules. Omni ships token tracking; make it hierarchy-aware.
14. **Interop hedge: AGENTS.md.** Emit both CLAUDE.md and the emerging cross-vendor AGENTS.md (Codex/Cursor/Gemini-CLI read it), with CLAUDE.md as a thin include — your "other AI provider" requirement nearly for free.

## 8. Recommended architecture direction for ubiqo

**Control plane (self-hosted, Apache-2.0-style):** Postgres for identity/RBAC/hierarchy/memory-index/events (steal Omni's one-database ops posture; add pgvector when you want semantic recall).

**Four planes over it:**

1. **Context plane** — org → project → `{shared/, users/<u>/}` trees; per-level generated `CLAUDE.md`/`AGENTS.md` + `skills/` + `llms.txt` index. Shared trees are git repos (semver tags, provenance trailers); private trees are plain object storage.
2. **Memory plane** — provider interface, **Mem0 self-hosted as default** (independently validated by Omni's choice and by OpenMemory's sunset notice pointing there), namespaced per org/project/user, with the promotion-review workflow.
3. **Storage plane** — Postgres metadata + rclone/S3-compatible blob driver (content-addressed, GC'd, like Omni).
4. **Distribution plane** — the **remote MCP server** (tools: `get_context`, `search`, `remember/recall`, `read/write_artifact`, `release`, `activity`, `lock`) + the bootstrap **plugin marketplace** + **hooks** for session-boundary sync; OAuth device flow mapping clients to ubiqo identities.

**RBAC:** roles at org (owner/admin/member/guest) and project (maintainer/contributor/viewer) enforced in the MCP tool layer and mirrored into git permissions; private user folders truly private (ACL now, per-user encryption later).

**Sequencing that de-risks it:** (1) MCP server + hierarchy + git-backed shared folders — this alone solves both of your problem statements; (2) memory plane + promotion workflow; (3) plugin/hooks distribution + activity feed; (4) optional search substrate — and *this* is where Omni re-enters, indexed over your artifact store and org connectors, wrapped behind your MCP server. Revisit Omni if it ever ships an MCP server endpoint (watch its releases; at its cadence, check monthly).

## 9. Key sources

**Omni (first-party):** [repo](https://github.com/getomnico/omni) · [LICENSE](https://raw.githubusercontent.com/getomnico/omni/main/LICENSE) · [releases](https://github.com/getomnico/omni/releases) · [architecture](https://docs.getomni.co/architecture) · [access control](https://docs.getomni.co/user-guide/access-control.md) · [user management](https://docs.getomni.co/admin/user-management.md) · [memory](https://docs.getomni.co/admin/memory.md) · [background agents](https://docs.getomni.co/admin/background-agents.md) · [configuration](https://docs.getomni.co/deployment/configuration.md) · [docs index (llms.txt)](https://docs.getomni.co/llms.txt) · [getomni.co](https://getomni.co/) · [Show HN thread](https://news.ycombinator.com/item?id=47215427)

**Landscape:** [Basic Memory repo](https://github.com/basicmachines-co/basic-memory) · [Basic Memory pricing](https://basicmemory.com/pricing) · [Basic Memory cloud docs](https://docs.basicmemory.com/cloud/cloud-guide) · [OpenMemory MCP announcement](https://mem0.ai/blog/introducing-openmemory-mcp) · [mem0 repo (sunset notice)](https://github.com/mem0ai/mem0) · [Omnigent repo](https://github.com/omnigent-ai/omnigent) · [Anthropic: Bringing memory to teams](https://claude.com/blog/memory) · [VentureBeat on Team/Enterprise memory](https://venturebeat.com/ai/anthropic-adds-memory-to-claude-team-and-enterprise-incognito-for-all) · [Computerworld on Team/Enterprise memory](https://www.computerworld.com/article/4056366/anthropic-adds-memory-to-claude-for-team-and-enterprise-plan-users.html) · memory-layer comparisons (directional): [Mem0 vs Zep vs Letta](https://rohitraj.tech/en/notes/open-source-ai-agent-memory-mem0-vs-zep-letta-2026) · [MCP.Directory comparison](https://mcp.directory/blog/mem0-vs-letta-vs-zep-vs-cognee-2026)

---

*Research run stats: 101 agents, 951 tool calls; 25 top-ranked claims adversarially verified with 3 skeptical votes each; 19 confirmed (17 unanimous), refuted claims discarded (including three "Postgres-only storage" claims and OpenMemory-as-current-recommendation). Requirement-2 storage details resolved from Omni's configuration reference directly.*
