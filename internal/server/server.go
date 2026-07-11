// Package server wires the MCP server and registers its tools.
package server

import (
	"context"
	"log"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	chdriver "github.com/corker/clickhouse-mcp/internal/clickhouse"
	"github.com/corker/clickhouse-mcp/internal/tools"
)

// New builds the MCP server and registers its tools. It runs the read-only
// write-probe: run_query is served only when the guard is verified to hold
// (fail-closed) — the inspection tools and ping are always served.
func New(ctx context.Context, name string, conn driver.Conn) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: name}, nil)
	tools.RegisterPing(s, conn)

	guardHolds, err := chdriver.WriteProbe(ctx, conn)
	switch {
	case err != nil:
		log.Printf("write-probe failed to run (%v); withholding run_query", err)
	case !guardHolds:
		log.Printf("write-probe: writes are NOT refused under readonly=2; withholding run_query. " +
			"Point the server at a read-only ClickHouse user, or check for a proxy stripping settings.")
	default:
		tools.RegisterRunQuery(s, conn)
	}
	return s
}
