# Backup & restore runbook

State lives in two places that must be restored **together** (ADR-0004):
PostgreSQL (control plane) and the git repos under `UBIQO_DATA_DIR/repos`
(artifact plane, the source of truth). `ubiqo backup` snapshots both into one
directory with a manifest.

## Take a backup

```sh
ubiqo backup --out /backups/ubiqo-$(date +%F)
# → db.sql, <org>__<project>.bundle per project, manifest.json
```

Run it from cron (daily) and copy the directory off-host. In Compose:
`docker compose exec ubiqo ubiqo backup --out /var/lib/ubiqo/backups/...`
then sync that volume path.

## Restore (ordered — git first, then DB, then reconcile)

1. Stop the server.
2. Recreate the repos from bundles (git is the source of truth):
   ```sh
   mkdir -p $UBIQO_DATA_DIR/repos/<org>
   git clone --bare <org>__<project>.bundle $UBIQO_DATA_DIR/repos/<org>/<project>.git
   ```
3. Restore the database:
   ```sh
   psql "$UBIQO_DATABASE_URL" -c 'drop schema public cascade; create schema public'
   psql "$UBIQO_DATABASE_URL" -f db.sql
   ```
4. Start the server, then reconcile projections against git:
   ```sh
   ubiqo fsck
   ```
   `fsck` re-inserts any release rows missing from the DB (e.g. releases cut
   after the DB dump but before the git bundle) and reports open proposals
   whose branches are gone.

## Drill

Quarterly: restore the latest backup into a scratch environment
(`docker compose -p ubiqo-drill up`), run `ubiqo fsck`, and confirm
`GET /readyz` plus one `ubiqo_get_context` call. A backup you haven't
restored is a hope, not a backup.

## Failure-mode matrix (what breaks when a dependency is down)

| Down | Effect | Client experience |
|---|---|---|
| Postgres | full outage (readyz fails) | hooks fail open: cached context + OFFLINE line |
| Git data dir | writes fail; reads of DB-only data OK | write/read_artifact tools error with reasons |
| ubiqo server | everything | hooks fail open; MCP tools absent from session |
