# ADR-0007: Memory = native Postgres FTS first; mem0 as P2 provider

**Status:** accepted · 2026-07-13

P0 `remember`/`recall` are explicit tools writing scoped rows (user/project/org) to Postgres with tsvector FTS + recency ranking — no extraction, no embeddings, no LLM keys server-side. `MemoryProvider` interface from day one; mem0 self-hosted becomes the opt-in P2 provider (separate compose profile, separate schema, non-fatal degradation), adding automatic extraction and semantic recall. Promotion workflow (user → project/org with maintainer approval) lands with P1 governance.

**Why:** sysadmin (lone Python service breaks single-binary + airgap; runs someone's LLM bill), architect (pgvector drags an embedding pipeline into P0). Explicit memory still delivers the demo's cross-account recall.
