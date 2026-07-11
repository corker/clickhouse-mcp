// Package server wires the MCP server and registers its tools.
package server

import (
	"context"
	"log"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	chdriver "github.com/corker/clickhouse-mcp/internal/clickhouse"
	"github.com/corker/clickhouse-mcp/internal/config"
	"github.com/corker/clickhouse-mcp/internal/tools"
)

// New runs the write-probe: run_query is served only when the guard is verified
// to hold (fail-closed); ping is always served. cfg is threaded in so future
// tools can be gated on configuration (e.g. the write path via AllowWriteAccess).
func New(ctx context.Context, name string, cfg *config.Config, conn driver.Conn) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: name}, nil)
	tools.RegisterPing(s, conn)
	tools.RegisterListDatabases(s, conn)
	tools.RegisterListTables(s, conn)

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

	// TODO(write-path): when write tools land, register them here gated on
	// cfg.AllowWriteAccess. The flag is plumbed through now so that becomes a
	// single added branch rather than a signature change.
	_ = cfg.AllowWriteAccess

	return s
}
