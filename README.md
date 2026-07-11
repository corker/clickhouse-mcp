# clickhouse-mcp

Model Context Protocol server for ClickHouse, written in Go.

Single binary. Works with any ClickHouse deployment (OSS, ClickHouse Cloud,
self-hosted) — no sidecar, no custom ClickHouse build required.

**Status: v0.1 pre-release. stdio transport only.**

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

- **v0.1** — stdio tools: `list_databases`, `list_tables`, `run_query`, `run_statement`; row + byte
  caps with truncation reason. Authorization delegated to ClickHouse RBAC.
- **v0.2** — HTTP transport, OAuth 2.0 with S256 PKCE, multi-provider IdP brokering (Google, Microsoft Entra ID, Keycloak, generic OIDC).
- **later** — DBA operational tools (`system.mutations`, `system.parts`, `system.replication_queue` rollups).

## Install

Requires Go 1.24+.

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

## Related projects

| Project | Language | Focus |
|---|---|---|
| [`ClickHouse/mcp-clickhouse`](https://github.com/ClickHouse/mcp-clickhouse) | Python | Official reference; includes chDB. |
| [`Altinity/altinity-mcp`](https://github.com/Altinity/altinity-mcp) | Go | Broadest feature set today; OAuth broker requires companion sidecar (`ch-jwt-verify`) or Antalya-25.8's `token_processors`. |
| `corker/clickhouse-mcp` (this project) | Go | Single binary, no sidecar, works with vanilla OSS ClickHouse. |

If you need OAuth broker + dynamic view-tools today, use Altinity's server.
This project prioritises a small dependency surface and a shorter path from
`go install` to a working MCP endpoint.

## License

MIT. See [LICENSE](LICENSE).
