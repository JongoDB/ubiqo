# ADR-0004: Git is authoritative for artifacts, proposals, releases

**Status:** accepted · 2026-07-13

Proposal = branch (`u/<user>/<artifact>`), release = annotated tag (`artifacts/<name>/vX.Y.Z`) + notes. Postgres rows for proposals/releases are rebuildable projections; mutation order is git-first, then DB; a startup reconciler rebuilds projections from refs and flags orphans (`ubiqo fsck`).

**Why:** dual sources of truth drift on crash between commit and insert (architect). Git-first also makes backup/restore consistency tractable (sysadmin blocker): `ubiqo backup` = pg_dump + per-project `git bundle` + manifest of repo SHAs.
