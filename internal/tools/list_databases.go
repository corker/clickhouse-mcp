package tools

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	chdriver "github.com/corker/clickhouse-mcp/internal/clickhouse"
)

type listDatabasesArgs struct{}

type listDatabasesOutput struct {
	Databases []string `json:"databases" jsonschema:"names of all databases on the server"`
}

func RegisterListDatabases(server *mcp.Server, conn driver.Conn) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_databases",
		Description: "Start here: list the databases on the ClickHouse server. " +
			"Use list_tables next to see a database's tables.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listDatabasesArgs) (*mcp.CallToolResult, listDatabasesOutput, error) {
		return listDatabases(ctx, conn)
	})
}

func listDatabases(ctx context.Context, conn driver.Conn) (*mcp.CallToolResult, listDatabasesOutput, error) {
	qctx := chdriver.DefaultReadContext(ctx)

	rows, err := conn.Query(qctx, "SELECT name FROM system.databases ORDER BY name")
	if err != nil {
		return nil, listDatabasesOutput{}, fmt.Errorf("list databases: %w", err)
	}
	defer func() { _ = rows.Close() }()

	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, listDatabasesOutput{}, fmt.Errorf("scan database name: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, listDatabasesOutput{}, fmt.Errorf("read databases: %w", err)
	}
	return nil, listDatabasesOutput{Databases: names}, nil
}
