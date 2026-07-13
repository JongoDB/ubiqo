# ADR-0003: P0 auth = opaque device tokens; OAuth 2.1 AS deferred to P1 (ory/fosite)

**Status:** accepted · 2026-07-13

P0 ships opaque bearer device tokens: issued via CLI/admin (`ubiqo device create`), SHA-256-hashed at rest, one row per device with label + last-seen, individually revocable, invalidated on membership removal. Claude Code/CLI/hooks attach with an Authorization header. The OAuth 2.1 authorization server (PKCE, dynamic client registration, refresh rotation, RFC 7009/8414/9728) required by claude.ai Desktop connectors is P1, built on ory/fosite — never hand-rolled (security blocker).

**Why:** the AS is a hidden L misfiled as part of an M (architect); the MCP Go SDK only verifies tokens. Device tokens cover every P0 client and keep the surface auditable. Honest limitation documented: Desktop/claude.ai connectors onboard in P1.
