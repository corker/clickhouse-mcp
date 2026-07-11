package tools

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	chdriver "github.com/corker/clickhouse-mcp/internal/clickhouse"
)

type listTablesArgs struct {
	Database string `json:"database" jsonschema:"the database to list tables from"`
	Table    string `json:"table,omitempty" jsonschema:"if set, return only this table with its full column schema"`
	Columns  bool   `json:"include_columns,omitempty" jsonschema:"if true, fold each table's column schema in (use for small databases)"`
}

// tableInfo is the lean per-table row. Columns is populated only when the caller
// asked for schema (via table= or include_columns). RowCount is null for engines
// that do not track rows (e.g. views).
type tableInfo struct {
	Name     string   `json:"name"`
	Engine   string   `json:"engine"`
	RowCount *uint64  `json:"row_count" jsonschema:"total rows, or null for engines that do not track it (e.g. views)"`
	Comment  string   `json:"comment,omitempty"`
	Columns  []column `json:"columns,omitempty" jsonschema:"column schema, present only when requested"`
}

type column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type listTablesOutput struct {
	Database string      `json:"database"`
	Tables   []tableInfo `json:"tables"`
}

func RegisterListTables(server *mcp.Server, conn driver.Conn) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_tables",
		Description: "List a database's tables (name, engine, row count). Pass table= " +
			"for one table's full column schema, or include_columns=true to fold " +
			"schema for every table (small databases only).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args listTablesArgs) (*mcp.CallToolResult, listTablesOutput, error) {
		return listTables(ctx, conn, args)
	})
}

func listTables(ctx context.Context, conn driver.Conn, args listTablesArgs) (*mcp.CallToolResult, listTablesOutput, error) {
	if args.Database == "" {
		return nil, listTablesOutput{}, fmt.Errorf("database is required (call list_databases to see the options)")
	}
	qctx := chdriver.ReadOnlyCappedContext(ctx,
		chdriver.DefaultMaxResultRows, chdriver.DefaultMaxResultBytes, chdriver.DefaultMaxExecSeconds)

	tables, err := leanTables(qctx, conn, args.Database, args.Table)
	if err != nil {
		return nil, listTablesOutput{}, err
	}
	// table= addresses one table; a miss is a clear not-found, not an empty list.
	if args.Table != "" && len(tables) == 0 {
		return nil, listTablesOutput{}, fmt.Errorf("table %q not found in database %q", args.Table, args.Database)
	}

	if args.Table != "" || args.Columns {
		for i := range tables {
			cols, err := tableColumns(qctx, conn, args.Database, tables[i].Name)
			if err != nil {
				return nil, listTablesOutput{}, err
			}
			tables[i].Columns = cols
		}
	}
	return nil, listTablesOutput{Database: args.Database, Tables: tables}, nil
}

func leanTables(ctx context.Context, conn driver.Conn, database, table string) ([]tableInfo, error) {
	sql := "SELECT name, engine, total_rows, comment FROM system.tables WHERE database = ?"
	qargs := []any{database}
	if table != "" {
		sql += " AND name = ?"
		qargs = append(qargs, table)
	}
	sql += " ORDER BY name"

	rows, err := conn.Query(ctx, sql, qargs...)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []tableInfo
	for rows.Next() {
		var t tableInfo
		if err := rows.Scan(&t.Name, &t.Engine, &t.RowCount, &t.Comment); err != nil {
			return nil, fmt.Errorf("scan table: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func tableColumns(ctx context.Context, conn driver.Conn, database, table string) ([]column, error) {
	rows, err := conn.Query(ctx,
		"SELECT name, type FROM system.columns WHERE database = ? AND table = ? ORDER BY position",
		database, table)
	if err != nil {
		return nil, fmt.Errorf("describe %s.%s: %w", database, table, err)
	}
	defer func() { _ = rows.Close() }()

	var cols []column
	for rows.Next() {
		var c column
		if err := rows.Scan(&c.Name, &c.Type); err != nil {
			return nil, fmt.Errorf("scan column: %w", err)
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}
