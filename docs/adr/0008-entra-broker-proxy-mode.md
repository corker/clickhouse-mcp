# Layer 3: an in-server OAuth broker for Entra (proxy mode), on the trino/tesseract pattern

**Status:** accepted (2026-07-12) — refines [ADR-0007](0007-auth-layered-resource-server-plus-optional-broker.md)

ADR-0007 left the interactive broker as an "optional layer," open on whether it lives in the server
or in front of it. The requirement is that **Entra ID works smoothly out of the box** — an operator
deploying to Azure Container Apps should not have to stand up a second component (Cloudflare Worker,
APIM). So the broker lives **in the server**, enabled by `MCP_AUTH_MODE=broker`, built on the
pattern proven by `tuannvm/oauth-mcp-proxy` (the library behind mcp-trino) and equivalent
tesseract-style brokers.

## Why an in-server broker (not a fronting gateway)

The MCP authorization spec makes interactive clients (claude.ai, Cursor, `mcp-remote`) run a fixed
discovery sequence: `401` → Protected Resource Metadata (RFC 9728) → Authorization Server Metadata
(RFC 8414) → **Dynamic Client Registration** (RFC 7591, `POST /register`) → PKCE auth-code. Entra
satisfies none of the last three cleanly:

- **No DCR / no `/register`.** Entra only supports pre-registered apps.
- **Non-standard metadata.** Entra serves `openid-configuration`, not the RFC 8414
  `oauth-authorization-server` document clients look for, and its doc omits fields clients require
  (e.g. `code_challenge_methods_supported`).
- **No `resource` parameter** (RFC 8707) support.

The battle-tested answer is a small broker that **synthesizes the spec-shaped endpoints in front of
Entra**. The community consensus ("treat the MCP server as a resource server, add a proxy for
arbitrary clients") deploys that proxy as a separate component — but the requirement here is
zero-extra-infra Entra, so we adopt the same proxy *logic* inside the server, gated behind a mode so
it is off by default and never touches the `off`/`bearer` paths.

## The two modes (from the reference)

A single `MCP_AUTH_MODE` selects behavior; every proxy handler re-checks the mode (defense in depth):

- **bearer** (ADR-0007, shipped): resource-server token validation only. Metadata, if served, points
  clients at the upstream IdP's *own* endpoints — which is sufficient for DCR-capable, directly
  reachable IdPs (Keycloak, Okta, Google, Auth0). No proxy endpoints.
- **broker**: bearer validation **plus** the synthesized OAuth endpoints for Entra.

## Broker endpoints (proxy mode)

| Endpoint | Behavior |
| --- | --- |
| `/.well-known/oauth-protected-resource` | RFC 9728 PRM (SDK handler) |
| `/.well-known/oauth-authorization-server` | RFC 8414 metadata in the **client-expected shape**, adding the fields Entra omits, with `authorization_endpoint`/`token_endpoint`/`registration_endpoint` pointing at **this server** |
| `POST /oauth/register` | **Fake DCR**: accept any registration and return the operator's one **pre-registered Entra `client_id`**. This is the trick that satisfies the client's mandatory DCR step without Entra supporting DCR. |
| `GET /oauth/authorize` | Redirect to Entra's real authorize endpoint with our `client_id`, **passing the client's PKCE `code_challenge` through unchanged** |
| `GET /oauth/callback` | Entra redirects here (the single URI Entra knows); verify the signed state, then redirect back to the client's real `redirect_uri` |
| `POST /oauth/token` | Exchange the code with Entra **server-to-server** (the broker holds the Entra client secret), return the token to the client |

## The security model — fixed-redirect + signed state (the crux)

Redirect-URI handling is where OAuth brokers get "one-click account takeover" bugs, and it is the
part the reference has the most hardening and dedicated tests around. We adopt its model rather than
rediscover it:

- **Fixed redirect at Entra.** The broker registers **one** redirect URI with Entra — its own
  `/oauth/callback`. This matches Entra's pre-authorized-client constraint (Entra needs exactly one
  URI) and means client callback URLs are never registered in Entra.
- **Signed state carries the client's real `redirect_uri`.** Before redirecting to Entra, the broker
  packs `{client redirect_uri, original state, nonce, timestamp}` into an **HMAC-signed** `state`
  blob. On callback it verifies the signature before trusting the return address, so a tampered state
  cannot redirect the code to an attacker.
- **Seven-guard client `redirect_uri` validation** before proxying: non-empty; parseable;
  scheme is http/https only; HTTPS enforced except loopback; no URL fragment; **host is localhost or
  an operator-configured allowed-domain suffix** (the open-redirect defense); fail closed if no
  redirect config exists.
- **Ported hardening:** DoS caps (256 KB body on `/register`, field-length caps on OAuth params),
  CORS on the browser-hit endpoints, `state` nonce+timestamp against replay.

## Operator experience (the requirement)

Smooth Entra = register **one** app in Entra by hand (unavoidable — Entra has no DCR; this is
Microsoft's own pre-authorized-client model), then set env vars and flip the mode:
`MCP_AUTH_MODE=broker`, `OIDC_ISSUER` (the Entra tenant), the Entra `client_id`/`client_secret`, this
server's public URL, and the allowed client-redirect domain suffix(es). No second component.

## Considered options

- **Fronting gateway (Cloudflare Worker / APIM), broker not in the server** — rejected for the
  primary requirement: it is a second component to deploy, which is not "smooth Entra." Still a valid
  operator choice for `bearer` mode, documented as such.
- **Metadata-only bridge, no `/register`/token proxy** — rejected as insufficient for Entra: it
  works for DCR-capable IdPs (and *is* the `bearer`-mode metadata behavior), but Entra's missing DCR
  and token-endpoint constraints need the proxy. Kept as the `bearer`-mode subset.
- **Allowlist of client redirect URIs passed through to Entra** — rejected: requires every client
  callback registered in Entra and multi-URI Entra config; less smooth and larger open-redirect
  surface than fixed-redirect + signed state.

## Consequences

- The broker is a real OAuth authorization-server surface in our binary — the most security-sensitive
  code in the project. It is built in slices (metadata + fake-DCR first; the authorize/callback/token
  proxy with the redirect guards second) and ported deliberately from the reference's security
  checklist, not reinvented.
- `bearer` mode is unchanged and remains the default for spec-compliant IdPs. `broker` mode is
  additive and off unless configured.
- The token the client ultimately presents is still Entra's own JWT, validated by the same layer-2
  verifier (signature/issuer/expiry/audience). The broker changes only *how the client obtains* the
  token, not how it is validated.
- Requires validation against a real Entra tenant or a faithful Entra mock (missing-DCR,
  non-standard-metadata) before shipping — the fake-issuer harness from layer 2 is extended for this.
