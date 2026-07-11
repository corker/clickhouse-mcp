# clickhouse-mcp — instructions for Claude Code

Model Context Protocol server for ClickHouse, written in Go. Single binary.
Runs over stdio today; HTTP + OAuth/PKCE planned for v0.2.

## Layout

- `cmd/clickhouse-mcp/main.go` — thin entry: config → driver → server → run.
- `internal/config` — env-var config (`CLICKHOUSE_*`), no external deps.
- `internal/clickhouse` — driver init (`clickhouse-go/v2` native interface).
- `internal/server` — MCP server wiring; registers tools.
- `internal/tools` — one file per tool (`ping.go`, later `list_databases.go`, …).

No `pkg/`. This is a binary, not a library.

## Working here

- Toolchain is pinned via mise (`mise.toml` → Go 1.26). Run `mise install` after cloning.
- Shell activation is **not assumed** — prefix Go commands with `mise exec --`
  (e.g. `mise exec -- go test ./...`). If your shell has `eval "$(mise activate zsh)"`,
  you can drop the prefix.
- Run `mise exec -- go test ./...` and `mise exec -- go vet ./...` before pushing.
- Lint: `mise exec -- golangci-lint run` (config in `.golangci.yml`, v2 format).
- Format: `mise exec -- gofmt -s -w .` and `mise exec -- goimports -w .`.
- Don't push to `main` without asking.
- Don't commit `go.sum` conflicts by hand — always `mise exec -- go mod tidy` first.

## Adding a tool

1. Create `internal/tools/<name>.go` with a `Register<Name>(server, conn)` function.
2. Follow the `ping.go` pattern: typed args struct + `mcp.AddTool` closure.
3. Wire it in `internal/server/server.go`.
4. Keep tools read-only unless `CLICKHOUSE_ALLOW_WRITE_ACCESS=true` (write path
   not implemented yet — leave TODO markers, not stubs).

## MCP SDK notes

Using `github.com/modelcontextprotocol/go-sdk` v1.6.x. Canonical example:
`examples/server/hello/main.go` in that repo. The SDK derives JSON schemas
from `json` and `jsonschema` struct tags on args types — keep args structs
close to the handler.

## ClickHouse driver notes

`github.com/ClickHouse/clickhouse-go/v2` native interface (`clickhouse.Open`),
not `database/sql`. Query with `conn.Query(ctx, ...)` / `conn.QueryRow(ctx, ...)`.
Reference: https://clickhouse.com/docs/en/integrations/go
