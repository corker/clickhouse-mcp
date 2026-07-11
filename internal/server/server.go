package server

import (
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/corker/clickhouse-mcp/internal/tools"
)

func New(name string, conn driver.Conn) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: name}, nil)
	tools.RegisterPing(s, conn)
	return s
}
