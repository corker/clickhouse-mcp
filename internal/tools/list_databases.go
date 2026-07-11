package tools

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	chdriver "github.com/corker/clickhouse-mcp/internal/clickhouse"
)

// DefaultDatabaseLimit keeps a many-database (e.g. multi-tenant) server from
// flooding the caller's context.
const DefaultDatabaseLimit = 200

type listDatabasesArgs struct {
	Limit int `json:"limit,omitempty" jsonschema:"max databases to return; defaults to 200"`
}

type listDatabasesOutput struct {
	Databases []string `json:"databases" jsonschema:"names of databases on the server"`
	Truncated bool     `json:"truncated" jsonschema:"true if the server has more databases than were returned"`
	Limit     int      `json:"limit" jsonschema:"the applied limit"`
	Note      string   `json:"note,omitempty" jsonschema:"guidance when the list was truncated"`
}

func RegisterListDatabases(server *mcp.Server, conn driver.Conn) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_databases",
		Description: "Start here: list the databases on the ClickHouse server. " +
			"Use list_tables next to see a database's tables.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args listDatabasesArgs) (*mcp.CallToolResult, listDatabasesOutput, error) {
		return listDatabases(ctx, conn, args)
	})
}

func listDatabases(ctx context.Context, conn driver.Conn, args listDatabasesArgs) (*mcp.CallToolResult, listDatabasesOutput, error) {
	limit := resolveLimit(args.Limit, DefaultDatabaseLimit)
	qctx := chdriver.DefaultReadContext(ctx)

	// Fetch limit+1 to detect that more exist beyond the cut.
	rows, err := conn.Query(qctx, "SELECT name FROM system.databases ORDER BY name LIMIT ?", limit+1)
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

	out := listDatabasesOutput{Limit: limit}
	names, out.Truncated, out.Note = truncate(names, limit, "databases")
	out.Databases = names
	return nil, out, nil
}
