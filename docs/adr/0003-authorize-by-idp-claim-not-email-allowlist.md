# Authorize by IdP claim, not an email allowlist

**Status:** accepted

Access control is decided from what the IdP asserts about the user, not from a list in our source.
Every request first passes mandatory token validation (`iss`, `aud`, `exp`). The coarse allow/deny
gate is then a **configurable group/role claim**: `OIDC_REQUIRED_CLAIM` (e.g. `groups`) must
contain `OIDC_REQUIRED_VALUE` (e.g. `clickhouse-mcp-users`). To grant or revoke a person, the
operator edits **group membership in their IdP** — no code change, no redeploy. Fine-grained,
tool-level gating uses **OAuth scopes** (`clickhouse:read` for `run_query` and the inspection
tools; a future `clickhouse:write` gates the write path behind `CLICKHOUSE_ALLOW_WRITE_ACCESS`).

## Considered options

- **Hardcoded email allowlist** (the `mcp-trino`/`mcp-tesseract` `ALLOWED_EMAILS` array) — rejected:
  the authorization decision lives in source, so changing who has access needs a code change and a
  redeploy (their own READMEs document this). It also can't react to offboarding in the IdP.
- **Externalized allowlist** (env var / mounted file instead of a source array) — kept only as a
  **fallback** for operators whose IdP cannot emit group/role claims. Editable without a rebuild,
  but still our list to maintain, not the IdP's.
- **Audience-only** (`aud` is the sole gate) — kept as the always-on baseline, but too coarse on its
  own when one IdP fronts many services.

## Why

The MCP authorization spec and current guidance treat authorization as an IdP/platform concern:
validate the token, then decide from a claim the IdP already issued (group, role, or scope). This
keeps identity governance (SSO, audit, offboarding) where it already lives, generalizes across OIDC
providers (only the claim *name* varies — config, per ADR-0002), and needs no redeploy to change
access. Scopes give a clean seam to gate read vs. write tools, unifying with the
`CLICKHOUSE_ALLOW_WRITE_ACCESS` intent from ADR-0001.

## Consequences

- Operators must create a group/role (and, for tool gating, scopes) in their IdP and configure the
  claim name/value. This is the intended trade: setup in the IdP, zero maintenance in our source.
- The claim name is configurable because `groups` vs `roles` and value formats differ per provider.
