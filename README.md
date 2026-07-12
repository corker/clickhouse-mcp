# clickhouse-mcp

Model Context Protocol server for ClickHouse, written in Go.

Single binary. Works with any ClickHouse deployment (OSS, ClickHouse Cloud,
self-hosted) — no sidecar, no custom ClickHouse build required.

**Status: v0.2. stdio + HTTP transport, with optional OAuth 2.1 bearer/broker auth.**

## What's there today

- `ping` — issues `SELECT 1` to verify the connection.
- `list_databases` / `list_tables` — inspect the schema (databases, tables, engines, row counts,
  columns), with explicit truncation so a large server can't flood the caller's context.
- `run_query` — runs a single row-returning query (SELECT/WITH/SHOW/DESCRIBE/EXPLAIN/EXISTS) and
  returns typed rows plus each column's type, with `LIMIT n+1` truncation detection. Large integers
  and decimals are returned as strings to avoid JSON precision loss.
- `run_statement` — executes a single statement that does not return rows (INSERT, ALTER, CREATE,
  DROP, …) and reports rows written. **Only usable if the connected ClickHouse user has the
  privilege** — see Security.

## Security — authorization is ClickHouse's, not the server's

This server does **not** decide what you may run. Every statement is executed against ClickHouse
under the user you configure, and **that user's privileges are the only authorization boundary**
(see [ADR-0006](docs/adr/0006-clickhouse-rbac-is-the-authorization-boundary.md)).

- **For a read-only deployment, point the server at a `GRANT SELECT`-only ClickHouse user.** Then
  `run_statement` writes are refused by ClickHouse, verifiably, regardless of what SQL is sent.
- Pointing it at a full-privilege user (e.g. an unrestricted `default`) means **the tools can
  write** — by design. There is no server-side read-only switch; a ClickHouse-setting guard such as
  `readonly=2` is *not* a boundary (an in-query `SETTINGS readonly=0` overrides it — verified). Use
  a privilege grant.

## Roadmap

- **v0.1** — the stdio tools above, with row/byte caps and ClickHouse-RBAC authorization.
- **v0.2 (done)** — HTTP transport and optional OAuth auth, see [HTTP transport & auth](#http-transport--auth).
- **next** — DBA operational tools (`system.mutations`, `system.parts`, `system.replication_queue` rollups).

## Install

Requires Go 1.25+.

```sh
go install github.com/corker/clickhouse-mcp/cmd/clickhouse-mcp@latest
```

Or clone and build. Toolchain is pinned via [mise](https://mise.jdx.dev):

```sh
git clone https://github.com/corker/clickhouse-mcp
cd clickhouse-mcp
mise install                                # installs Go 1.26 per mise.toml
mise exec -- go build ./cmd/clickhouse-mcp  # or drop the prefix if mise is shell-activated
```

## Use with Claude Code

Copy the shipped `.mcp.json` (project-scope config) or run:

```sh
claude mcp add --transport stdio clickhouse-mcp -- clickhouse-mcp
```

Then set the connection env vars in your shell or in `.mcp.json`'s `env` block:

| Variable | Default | Notes |
|---|---|---|
| `CLICKHOUSE_HOST` | `localhost` | |
| `CLICKHOUSE_PORT` | `9000` | Native TCP. Use `8443` / `9440` for TLS, `8123` for HTTP. |
| `CLICKHOUSE_USER` | `default` | |
| `CLICKHOUSE_PASSWORD` | *(empty)* | |
| `CLICKHOUSE_DATABASE` | `default` | |
| `CLICKHOUSE_SECURE` | `false` | Set `true` for TLS (ClickHouse Cloud). |

Reference: [Claude Code MCP docs](https://code.claude.com/docs/en/mcp).

## HTTP transport & auth

stdio (the default) serves a single local client and needs no auth. To serve remote clients over
HTTP, set `MCP_TRANSPORT=http` and pick an auth mode with `MCP_AUTH_MODE`. Design:
[ADR-0007](docs/adr/0007-auth-layered-resource-server-plus-optional-broker.md) (resource server) and
[ADR-0008](docs/adr/0008-entra-broker-proxy-mode.md) (broker).

| Variable | Default | Notes |
|---|---|---|
| `MCP_TRANSPORT` | `stdio` | `http` to serve over HTTP. |
| `MCP_HTTP_ADDR` | `:8080` | Listen address for HTTP. |
| `MCP_AUTH_MODE` | `off` | `off` (dev), `bearer`, or `broker`. |

**`bearer`** — validate an OAuth 2.1 access token on every request (the server is a pure resource
server; clients obtain tokens elsewhere). Authorization stays ClickHouse's; these gate *who* reaches
the tools.

| Variable | Required | Notes |
|---|---|---|
| `OIDC_ISSUER` | yes | Issuer URL; endpoints (incl. JWKS) resolved by discovery. |
| `MCP_RESOURCE_URI` | yes | This server's canonical identifier; a token's `aud` must equal it (RFC 8707). |
| `OIDC_IDENTITY_CLAIM` | no | Claim used as the user's identity (default `email`). |
| `OIDC_REQUIRED_CLAIM` / `OIDC_REQUIRED_VALUE` | no | Optional access gate; both or neither. |

**`broker`** — everything `bearer` does, plus an in-binary interactive OAuth broker so browser clients
(Claude.ai, IDEs) can log in even against an IdP that lacks Dynamic Client Registration (notably Entra).
Set `MCP_PUBLIC_URL` (this server's externally reachable base URL) and choose a provider:

| `MCP_BROKER_PROVIDER` | Provider vars | Endpoints |
|---|---|---|
| `entra` | `AZURE_TENANT_ID`, `AZURE_CLIENT_ID`, `AZURE_CLIENT_SECRET` | Derived from the tenant id. |
| `google` | `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | Fixed Google OAuth endpoints. |
| `generic` | `OIDC_ISSUER`, `MCP_RESOURCE_URI`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_AUTHORIZE_URL`, `OIDC_TOKEN_URL` | All explicit (any OIDC IdP, e.g. Keycloak). |

For a named provider the audience defaults to the client id; override with `MCP_RESOURCE_URI`.
`MCP_ALLOWED_REDIRECT_HOSTS` (comma-separated) allows non-loopback client redirect hosts (e.g.
`claude.ai`); loopback is always allowed. `OIDC_SCOPES` defaults to `openid profile email`.

## Related projects

| Project | Language | Focus |
|---|---|---|
| [`ClickHouse/mcp-clickhouse`](https://github.com/ClickHouse/mcp-clickhouse) | Python | Official reference; includes chDB. |
| [`Altinity/altinity-mcp`](https://github.com/Altinity/altinity-mcp) | Go | Broadest feature set today, incl. dynamic view-tools; its OAuth broker needs a companion sidecar (`ch-jwt-verify`) or Antalya-25.8's `token_processors`. |
| `corker/clickhouse-mcp` (this project) | Go | Single binary — bearer auth and an interactive OAuth broker are built in, no sidecar; works with vanilla OSS ClickHouse. |

If you need dynamic view-tools today, use Altinity's server. This project prioritises a small
dependency surface, an in-binary auth broker (no companion service), and a shorter path from
`go install` to a working MCP endpoint.

## License

MIT. See [LICENSE](LICENSE).
