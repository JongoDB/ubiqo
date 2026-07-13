# ADR-0006: P0 recut around the 15-minute demo

**Status:** accepted · 2026-07-13

**P0 keeps:** hierarchy engine; device tokens; git-backed shared/ with write/propose/merge/release; context compiler (pure function, content-hash version, identity injected at fetch); activity events + digest; thin native memory (remember/recall); SessionStart/SessionEnd hooks + minimal bootstrap plugin + MCP-snippet onboarding; solo-mode defaults (single-member org writes direct to main, no ceremony); `serve --local` no-TLS eval path; /healthz /readyz /metrics, JSON logs, /v1/meta version handshake; `ubiqo backup/restore` + `fsck`; seed fixture = the "day at Acme" walkthrough.

**P0 cuts (all behind interfaces/tool names, no doors closed):** pgvector/hybrid search and the `search` tool (FTS-only recall stays); rclone driver (BlobStore interface with local-fs + S3 impls); SeaweedFS bundling; artifact locks; Casbin; mem0; OAuth AS; console; roaming profiles.

**Why:** product/UX blockers — the aha (cross-account narration, no re-explaining) and the distribution surface ARE the product; a fabric without hooks demos as a slower git repo. Cut list per architect. Solo user is the founding problem and must be first-class day one.
