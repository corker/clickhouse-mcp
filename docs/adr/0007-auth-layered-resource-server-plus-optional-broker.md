# v0.2 auth: a bearer-token resource server, with an optional interactive broker layer

**Status:** accepted (2026-07-12) — **supersedes [ADR-0002](0002-v02-auth-generic-oidc-broker.md)**, refines [ADR-0003](0003-authorize-by-idp-claim-not-email-allowlist.md)

ADR-0002 chose a single OAuth **broker** as the primary v0.2 model. Mapping the actual set of
consumers that must reach the server — and verifying the MCP 2025-06-18 authorization spec, the
go-sdk `auth` package, and the real Azure/Entra deployment gaps — shows the decision is not
broker-vs-resource-server. It is **layered**:

- a **resource-server core** that validates a bearer token on every request (serves every consumer,
  because every consumer ultimately presents a token), and
- an **optional interactive broker layer** (protected-resource + authorization-server metadata,
  Dynamic Client Registration, token-exchange proxy) that the interactive PKCE clients need — and
  that real IdPs like Entra force, because Entra is not a spec-compliant MCP authorization server.

## The consumer matrix (why one flow cannot serve all)

Verified against vendor docs (2026-07):

| Consumer | Auth shape | Needs |
| --- | --- | --- |
| claude.ai custom connector (interactive) | OAuth 2.1 auth-code + PKCE/S256 with **user consent** | AS-facing flow; pure M2M `client_credentials` is **not** accepted as a connector flow |
| claude.ai connector (fixed credential) | static bearer / API key / custom header | header/token validation only |
| Cursor / VS Code / Claude Code / MCP Inspector | OAuth 2.1 + PKCE; **Cursor strictly enforces RFC 8414 + 7591 (DCR) + 9728** | full spec incl. DCR |
| Azure Container Apps + Entra ID | Entra has **no DCR**, serves non-standard metadata, Easy Auth blocks discovery | a bridging **broker/shim**, or platform termination |
| A2A (agent-to-agent) | Agent Card declares any of apiKey / Bearer / oauth2 / oidc / mtls; often **`client_credentials`** M2M | bearer/JWT validation, no interactive flow |
| local dev / Inspector | often none | an explicit no-auth mode |

Two facts collapse this into the layered model:

1. **Every consumer presents a bearer token to the actual MCP request** — whether obtained by
   interactive PKCE, a fixed header, or a client-credentials grant. So a resource-server validator is
   the common denominator that serves all of them.
2. **claude.ai forbids M2M `client_credentials` as a connector, but A2A relies on it.** A single
   prescribed OAuth flow therefore cannot serve both. The server must accept a *validated* token
   regardless of how it was obtained, and *separately* offer the interactive-flow assistance the PKCE
   clients need.

## Decision

- **Resource-server core (always on when auth is enabled).** The server is an OAuth 2.1 resource
  server: it validates the `Authorization: Bearer` token on every HTTP request and rejects with
  `401 + WWW-Authenticate` (RFC 9728) otherwise. Validation is: JWT signature via the IdP's JWKS
  (rotating), `iss` == `OIDC_ISSUER`, `exp`/`nbf`, and — critically — **`aud` == the server's own
  canonical resource URI (RFC 8707)**, so a token minted for another service cannot be replayed here
  (the confused-deputy / token-passthrough attack the spec forbids). This uses the go-sdk
  `auth.RequireBearerToken` middleware with a `TokenVerifier` we supply.
- **Authorization stays claim-based (ADR-0003 holds).** The identity and access-decision claims are
  read from the validated token's claims, not an allowlist we maintain. ADR-0003 is unchanged except
  that the claims come from a validated **access token**, not necessarily an ID token.
- **The ClickHouse boundary is unchanged (ADR-0006 holds).** Auth gates *access to the server*; what
  a request may *run* is still the connected ClickHouse user's privileges. Auth and RBAC are
  orthogonal layers.
- **Interactive broker layer is optional and additive.** When the target IdP is not a spec-compliant
  MCP authorization server (Entra), the server can serve the metadata + DCR `/register` + token-proxy
  shim that lets interactive clients complete PKCE. This is the ADR-0002 broker, re-scoped from
  "the primary model" to "the compatibility layer for clients/IdPs that need it."
- **Auth modes are explicit, selected by config:** (a) **off** — no auth (local dev / Inspector);
  (b) **bearer** — validate tokens, no interactive assistance (A2A, fixed-header claude.ai,
  pre-authed clients); (c) **broker** — bearer plus the interactive metadata/DCR/proxy layer
  (claude.ai interactive, Cursor, Entra).

## Considered options

- **Broker as the primary model (ADR-0002)** — superseded: it treated the AS-facing flow as the
  spine, but the spine is token *validation* (every consumer needs it); the interactive flow is a
  layer only some consumers need. Building broker-first over-serves A2A/fixed-header consumers and
  under-emphasizes the audience-validation the spec makes mandatory.
- **Pure resource server, no broker** — rejected as *sufficient*: it is correct where the IdP is
  MCP-spec-compliant, but **breaks against Entra** (no DCR, non-standard metadata, Easy Auth blocks
  discovery) — a primary deployment target. The broker layer is required for that case, so it cannot
  be dropped entirely; it is made optional instead.
- **Delegate entirely to platform (Azure Easy Auth / APIM in front)** — rejected as the *only* path:
  couples deployment to Azure, helps neither non-Azure hosts nor local dev, and still needs the
  bearer core for A2A/M2M. Kept as a valid *deployment choice* an operator may layer in front when
  auth mode is `bearer`.

## Consequences

- **Token format scope for the first cut: JWT access tokens.** JWKS validation requires a JWT.
  Opaque access tokens (some IdPs) would need RFC 7662 introspection (a per-request network call +
  client credentials) — deferred as an "extend by writing more code" follow-up, not built until a
  target IdP requires it. Covers Entra, Okta, Auth0, Keycloak, Google (with resource config).
- **`go-oidc/v3` is used for primitives, not its ID-token `Verifier`.** Its `RemoteKeySet` gives
  JWKS fetch + rotation and JWT signature/exp checks; but its `Config.ClientID` audience check has
  **ID-token semantics** (aud == client id), while we must check **access-token** audience
  (aud == resource URI, RFC 8707). So we do the audience check ourselves and use go-oidc only for
  the crypto/discovery plumbing.
- The server gains an HTTP transport (`mcp.StreamableHTTPHandler`) alongside stdio; stdio stays
  auth-free per the MCP spec (credentials come from the environment).
- The broker layer, when built, must obtain user consent per dynamically-registered client before
  proxying to a third-party IdP (the spec's confused-deputy requirement for proxy servers).
