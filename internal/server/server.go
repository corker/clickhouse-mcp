// Package server wires the MCP server and registers its tools.
package server

import (
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/corker/clickhouse-mcp/internal/tools"
)

// New registers every tool unconditionally. What the caller may actually run is
// enforced by ClickHouse against the connected user's privileges (ADR-0006), so
// the server does not gate tools by configuration.
func New(name string, conn driver.Conn) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: name}, nil)
	tools.RegisterPing(s, conn)
	tools.RegisterListDatabases(s, conn)
	tools.RegisterListTables(s, conn)
	tools.RegisterRunQuery(s, conn)
	tools.RegisterRunStatement(s, conn)
	return s
}
