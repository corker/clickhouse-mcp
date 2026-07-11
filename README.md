# clickhouse-mcp

Model Context Protocol server for ClickHouse, written in Go.

Single binary. Works with any ClickHouse deployment (OSS, ClickHouse Cloud,
self-hosted) — no sidecar, no custom ClickHouse build required.

**Status: v0.1 pre-release. Read-only, stdio transport only.**

## What's there today

- `ping` tool — issues `SELECT 1` against the configured ClickHouse to verify the connection.

That's it. This is a scaffolding baseline; read-only inspection tools
(`list_databases`, `list_tables`, `describe_table`, `run_query`) land next.

## Roadmap

- **v0.1** — read-only tools over stdio: `list_databases`, `list_tables`, `describe_table`, `run_query` (SELECT/SHOW/DESCRIBE/EXISTS/EXPLAIN/WITH only), server-side row + byte caps with truncation reason.
- **v0.2** — HTTP transport, OAuth 2.0 with S256 PKCE, multi-provider IdP brokering (Google, Microsoft Entra ID, Keycloak, generic OIDC).
- **later** — write access gated by `CLICKHOUSE_ALLOW_WRITE_ACCESS`, DDL by `CLICKHOUSE_ALLOW_DROP`, DBA operational tools (`system.mutations`, `system.parts`, `system.replication_queue` rollups).

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
