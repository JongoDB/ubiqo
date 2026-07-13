# ADR-0002: Artifact plane executes system git; no go-git

**Status:** accepted · 2026-07-13

All git mutations (commit, branch, merge, tag) shell out to the system `git` binary (require ≥ 2.38) operating on server-owned bare repos via short-lived local clones; reads use `git show`/`ls-tree`/`tag -l` directly against the bare repo. Everything sits behind an `ArtifactStore` interface; writes are serialized per-repo with an in-process lock.

**Why:** go-git cannot three-way merge or detect conflicts (fast-forward only), lacks gc/repack, and is slow at blame — `merge` as designed was unbuildable on it (architect + QA blockers). Exec-git gives battle-tested merge semantics, `merge-tree` conflict reports, and repo maintenance for free, and drops a large dependency. Server-mediated writes only; no server-side git hooks; symlinks and path traversal rejected at `write_artifact`.
