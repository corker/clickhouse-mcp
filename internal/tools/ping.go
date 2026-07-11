// Package tools implements the MCP tools exposed by the server.
package tools

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type pingArgs struct{}

// RegisterPing registers the ping tool, which verifies the ClickHouse connection.
func RegisterPing(server *mcp.Server, conn driver.Conn) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        "ping",
		Description: "Verify the ClickHouse connection by issuing SELECT 1.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ pingArgs) (*mcp.CallToolResult, any, error) {
		var n uint8
		if err := conn.QueryRow(ctx, "SELECT 1").Scan(&n); err != nil {
			return nil, nil, fmt.Errorf("select 1: %w", err)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("ok (%d)", n)}},
		}, nil, nil
	})
}
