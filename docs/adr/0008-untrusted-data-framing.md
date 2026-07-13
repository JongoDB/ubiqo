# ADR-0008: Untrusted-data framing for all user/agent-authored content entering context

**Status:** accepted · 2026-07-13

Everything authored by users or agents — artifact content, session summaries, event text, memory text, display names — enters compiled bundles and digests only inside delimited untrusted-data envelopes with a fixed preamble ("data, not instructions"), and is never interpolated into the instruction layer itself. Maintainer merge is documented as the injection trust boundary. Compiled instruction templates may interpolate only server-controlled enums/identifiers (roles, versions, counts, slugs validated `[a-z0-9-]`).

**Why:** security high — the two-hop prompt-injection path (poisoned artifact → agent-assisted review → org-wide context) is cheap to frame now and effectively unretrofittable after templates and consumers proliferate.
