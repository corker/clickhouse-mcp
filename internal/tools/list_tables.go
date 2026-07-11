package tools

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	chdriver "github.com/corker/clickhouse-mcp/internal/clickhouse"
)

// Caps that keep a large database from flooding the caller's context. The folded
// default is much tighter than the lean one because include_columns pulls every
// column of every table ("small databases only").
const (
	DefaultTableLimit       = 200
	DefaultFoldedTableLimit = 20
	MaxColumnsPerTable      = 200
)

type listTablesArgs struct {
	Database string `json:"database" jsonschema:"the database to list tables from"`
	Table    string `json:"table,omitempty" jsonschema:"if set, return only this table with its full column schema"`
	Columns  bool   `json:"include_columns,omitempty" jsonschema:"if true, fold each table's column schema in (use for small databases)"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max tables to return; defaults to 200"`
}

// tableInfo is a per-table row. RowCount is null for engines that do not track
// rows (e.g. views).
type tableInfo struct {
	Name             string   `json:"name"`
	Engine           string   `json:"engine"`
	RowCount         *uint64  `json:"row_count" jsonschema:"total rows, or null for engines that do not track it (e.g. views)"`
	Comment          string   `json:"comment,omitempty"`
	Columns          []column `json:"columns,omitempty" jsonschema:"column schema, present only when requested"`
	ColumnsTruncated bool     `json:"columns_truncated,omitempty" jsonschema:"true if the table has more columns than were returned"`
}

type column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type listTablesOutput struct {
	Database  string      `json:"database"`
	Tables    []tableInfo `json:"tables"`
	Truncated bool        `json:"truncated" jsonschema:"true if the database has more tables than were returned"`
	Limit     int         `json:"limit" jsonschema:"the applied table limit"`
	Note      string      `json:"note,omitempty" jsonschema:"guidance when the list was truncated"`
}

func RegisterListTables(server *mcp.Server, conn driver.Conn) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "list_tables",
		Description: "List a database's tables (name, engine, row count). Database " +
			"and table names are case-sensitive. Pass table= for one table's full " +
			"column schema, or include_columns=true to fold schema for every table " +
			"(small databases only).",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args listTablesArgs) (*mcp.CallToolResult, listTablesOutput, error) {
		return listTables(ctx, conn, args)
	})
}

func listTables(ctx context.Context, conn driver.Conn, args listTablesArgs) (*mcp.CallToolResult, listTablesOutput, error) {
	if args.Database == "" {
		return nil, listTablesOutput{}, fmt.Errorf("database is required (call list_databases to see the options)")
	}
	limit := resolveTableLimit(args.Limit, args.Columns)
	qctx := chdriver.DefaultReadContext(ctx)

	// table= addresses exactly one table, so the browse limit does not apply.
	// Otherwise fetch limit+1 to detect that more tables exist beyond the cut.
	fetch := limit + 1
	if args.Table != "" {
		fetch = 0
	}
	tables, err := leanTables(qctx, conn, args.Database, args.Table, fetch)
	if err != nil {
		return nil, listTablesOutput{}, err
	}

	// An empty result is ambiguous — the database may not exist (a common cause is
	// a case-mismatched name, since ClickHouse names are case-sensitive). Only when
	// empty, pay one small query to tell the caller which it is.
	if len(tables) == 0 {
		exists, err := databaseExists(qctx, conn, args.Database)
		if err != nil {
			return nil, listTablesOutput{}, err
		}
		if !exists {
			return nil, listTablesOutput{}, fmt.Errorf("database %q not found (names are case-sensitive; call list_databases to see the options)", args.Database)
		}
		if args.Table != "" {
			return nil, listTablesOutput{}, fmt.Errorf("table %q not found in database %q", args.Table, args.Database)
		}
	}

	out := listTablesOutput{Database: args.Database, Limit: limit}
	if args.Table == "" {
		tables, out.Truncated, out.Note = truncate(tables, limit, "tables")
	}

	if args.Table != "" || args.Columns {
		for i := range tables {
			cols, truncated, err := tableColumns(qctx, conn, args.Database, tables[i].Name)
			if err != nil {
				return nil, listTablesOutput{}, err
			}
			tables[i].Columns = cols
			tables[i].ColumnsTruncated = truncated
		}
	}
	out.Tables = tables
	return nil, out, nil
}

func databaseExists(ctx context.Context, conn driver.Conn, database string) (bool, error) {
	var n uint64
	if err := conn.QueryRow(ctx, "SELECT count() FROM system.databases WHERE name = ?", database).Scan(&n); err != nil {
		return false, fmt.Errorf("check database %q: %w", database, err)
	}
	return n > 0, nil
}

// leanTables lists tables ordered by name. fetch caps the rows (0 = unbounded,
// used for the single-table table= path).
func leanTables(ctx context.Context, conn driver.Conn, database, table string, fetch int) ([]tableInfo, error) {
	// Filter the write-probe's sentinel table defensively — WriteProbe drops it,
	// but this guarantees it never surfaces even if a drop failed.
	sql := "SELECT name, engine, total_rows, comment FROM system.tables WHERE database = ? AND name != ?"
	qargs := []any{database, chdriver.ProbeTable}
	if table != "" {
		sql += " AND name = ?"
		qargs = append(qargs, table)
	}
	sql += " ORDER BY name"
	if fetch > 0 {
		sql += " LIMIT ?"
		qargs = append(qargs, fetch)
	}

	rows, err := conn.Query(ctx, sql, qargs...)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []tableInfo{}
	for rows.Next() {
		var t tableInfo
		if err := rows.Scan(&t.Name, &t.Engine, &t.RowCount, &t.Comment); err != nil {
			return nil, fmt.Errorf("scan table: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// tableColumns caps at MaxColumnsPerTable so a very wide table cannot flood the
// caller; truncated reports whether more exist.
func tableColumns(ctx context.Context, conn driver.Conn, database, table string) (cols []column, truncated bool, err error) {
	rows, err := conn.Query(ctx,
		"SELECT name, type FROM system.columns WHERE database = ? AND table = ? ORDER BY position LIMIT ?",
		database, table, MaxColumnsPerTable+1)
	if err != nil {
		return nil, false, fmt.Errorf("describe %s.%s: %w", database, table, err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var c column
		if err := rows.Scan(&c.Name, &c.Type); err != nil {
			return nil, false, fmt.Errorf("scan column: %w", err)
		}
		cols = append(cols, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(cols) > MaxColumnsPerTable {
		return cols[:MaxColumnsPerTable], true, nil
	}
	return cols, false, nil
}
