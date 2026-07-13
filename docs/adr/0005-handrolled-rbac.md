# ADR-0005: Hand-rolled RBAC matrix; no Casbin

**Status:** accepted · 2026-07-13

A constant role→action map (org: owner/admin/member/guest; project: maintainer/contributor/viewer) behind `authz.Can(actor, scope, action) (bool, denial)`. Enforcement is deny-by-default in one tool interceptor; every registered MCP tool declares its required action; a registry test fails if any tool omits it; a table-driven role×tool negative matrix pins behavior. Denials are structured: `{reason, caller_role, allowed_alternative}`.

**Why:** two fixed scopes and seven fixed roles don't justify a policy DSL + adapters (architect); centralized interception kills the "one forgotten check" bypass class (QA, security). OpenFGA remains the documented scale-up path when custom roles/per-artifact grants arrive.
