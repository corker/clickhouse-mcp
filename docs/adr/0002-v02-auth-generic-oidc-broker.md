# v0.2 auth: generic OIDC broker via discovery, not per-IdP code

**Status:** superseded by [ADR-0007](0007-auth-layered-resource-server-plus-optional-broker.md) (2026-07-12)

> **Superseded.** Mapping the full consumer matrix (claude.ai interactive + fixed-header, Cursor/VS
> Code with strict DCR, A2A M2M, Azure/Entra, local dev) and verifying the MCP 2025-06-18 auth spec
> showed the primary model is a bearer-token **resource server**, with the broker demoted to an
> *optional* compatibility layer for interactive clients and non-compliant IdPs (Entra). The
> discovery-generalizes rationale and the "extend by writing more code for non-OIDC" scope boundary
> below still hold for that optional layer. See ADR-0007.

For v0.2 (HTTP transport), the server is an **OAuth 2.1 broker**: it presents itself as the
authorization server to the MCP client (runs the auth-code + PKCE/S256 flow, issues its own
access token), and delegates authentication upstream to a single external OIDC provider. The
upstream provider is **not** hardcoded — its endpoints are resolved at startup from
`{OIDC_ISSUER}/.well-known/openid-configuration` (OIDC Discovery), so Entra ID, Google, Keycloak,
Auth0, Okta, or any OIDC issuer works by **configuration, not code**. The end-user's identity is
read from a configurable ID-token claim (`OIDC_IDENTITY_CLAIM`, defaulting to `email` with a
`preferred_username` fallback for issuers like Entra that omit `email`).

The ClickHouse connection uses a **single service credential** (already in `internal/config`);
OAuth gates *access to the server*, it does not map end-users to per-user ClickHouse identities.
Every authorized query runs under that one connection, whose ClickHouse privileges are the
authorization boundary (ADR-0006).

## Considered options

- **Broker welded to one IdP** (the `mcp-trino`/`mcp-tesseract` reference pattern) — rejected: it
  hardcodes the authority URL, the `/oauth2/v2.0/*` endpoint paths, and Azure-specific claim
  handling. Adding a second IdP means editing code. It is Entra-specific only because it *skipped
  discovery*.
- **Pure Resource Server** (validate externally-issued tokens; PKCE lives entirely in the client)
  — rejected as the *primary* mode: MCP clients (Claude, VS Code) drive their OAuth flow against
  the MCP server as the authorization server; brokering to an upstream IdP the client can't reach
  directly is the feature that makes "just add the connector" work. (Token-validation logic is
  still reused internally.)
- **Per-IdP interface / plugin now** — rejected: premature abstraction for one implementation.
  Discovery makes one concrete code path cover all OIDC issuers; a second file is written only if a
  genuinely non-OIDC provider is ever needed (see scope note).

## Why (discovery generalizes ~95%)

OIDC endpoints, the auth-code + PKCE flow, and refresh are identical across OIDC providers; only
the endpoint URLs (→ discovery) and the identity claim (→ config) vary. So the whole broker is one
config-driven path. The downstream (client ↔ server) PKCE half is standard OAuth 2.1 and provider-
agnostic already.

## Scope boundary

This generalizes across **OIDC** providers, not bare OAuth 2.0 providers that lack a discovery
document and an ID token (e.g. classic GitHub OAuth). Those need a per-provider file and are out of
scope until actually required — the "extend by writing more code" escape hatch, used only for the
genuinely-different case.

## Consequences

- One upstream IdP is configured at a time (`OIDC_ISSUER`); "multi-provider" means "any OIDC
  issuer, by config", not "several brokered in-process at once".
- Token-signing choice (issue our own JWT) is deferred to implementation; prefer RS256 + published
  JWKS over the reference's HS256-with-client-secret for an OSS binary.
