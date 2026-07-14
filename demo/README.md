# Two-account demo (two containers = two machines)

Validates both founding problems with nothing but Docker and your two
claude.ai accounts: **cross-account awareness** (account B's Claude narrates
account A's work) and **cross-machine continuity** (a fresh filesystem
rehydrates the same shared state).

## Bring up the fabric

```sh
docker compose -f demo/docker-compose.yml up -d --build
docker compose -f demo/docker-compose.yml logs seed
```

The `seed` log prints two device tokens for the demo org `acme`
(project `website-redesign`, team mode):

- **jon** — contributor → use with **account A** on workstation-a
- **sam** — maintainer → use with **account B** on workstation-b

(Tokens print only on the first run. To reset everything:
`docker compose -f demo/docker-compose.yml down -v` and up again.)

## Attach the two workstations

Terminal A:
```sh
docker compose -f demo/docker-compose.yml exec workstation-a bash
attach.sh <jon-token>
claude          # sign in with claude.ai ACCOUNT A (URL + paste-code flow)
```

Terminal B — same, with the **sam** token and **account B** on `workstation-b`.

`attach.sh` does: `ubiqo login` → `ubiqo init` (binds the project dir) →
`claude mcp add` (the ubiqo connector) → installs the SessionStart/SessionEnd
hooks. Each Claude session now begins with
`ubiqo: context <version> loaded` plus the compiled instructions, team
activity, and memories.

## The script (≈15 minutes, stopwatch encouraged)

Run in order; the ✓ line is what "pass" looks like.

1. **B (sam):** `What happened in website-redesign while I was away?`
   ✓ Narrates jon's open api-design proposal, the brand-kit v0.1.0 release,
   and the shared memory (Tailwind v4) — none of which you told it.
2. **B (sam):** `Review jon's open proposal. If it's reasonable, merge it and cut a minor release.`
   ✓ Reviews the diff in chat, merges, releases `artifacts/api-design/v0.1.0`.
3. **A (jon):** `What's the latest on website-redesign?`
   ✓ Digest reports sam's merge + release of *jon's own* work — closed loop
   across two accounts.
4. **A (jon):** `Release brand-kit as a major version.`
   ✓ Claude relays a structured denial: contributor can't release, and the
   error itself teaches the correct next step. Governance moment — does the
   denial read as helpful or as a dead end?
5. **A (jon):** `Remember for the whole team: deploys happen Fridays only.`
   **B (sam):** `When do we deploy?` (fresh session)
   ✓ Sam's Claude recalls it via project memory.
6. **Failure honesty:** `docker compose -f demo/docker-compose.yml stop ubiqo`,
   then start a new session on either workstation.
   ✓ Session still starts, with a loud `ubiqo: OFFLINE — using STALE cached
   context` line. `start ubiqo` again; next session-end drains the spooled
   activity.

## Variants

- **Same account, two machines (continuity check):** run `attach.sh` with the
  *same* token on both workstations, sign in with the same account. Session
  summaries from laptop-a show up in laptop-b's next session digest.
- **Two real computers:** publish the port on the host running the fabric
  (`ports: ["8383:8383"]` on the `ubiqo` service), then on the other machine
  point `attach.sh` at it via `UBIQO_SERVER_URL=http://<host-or-tailnet-ip>:8383`.
  Plain-HTTP bearer is for trusted networks only — for anything public, use
  `deploy/` with the Caddy TLS profile.

## What to report back

Time-to-first-context (target: under 10 minutes to step 1), any step where a
Claude picked the wrong tool or misread a denial, and any moment where you
had to explain something the bundle should have taught it.
