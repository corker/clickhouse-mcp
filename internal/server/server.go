// Package server wires the MCP server and registers its tools.
package server

import (
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/corker/clickhouse-mcp/internal/tools"
)

// New builds the MCP server and registers all tools against conn.
func New(name string, conn driver.Conn) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: name}, nil)
	tools.RegisterPing(s, conn)
	return s
}
