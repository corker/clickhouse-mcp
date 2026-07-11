package tools

import (
	"context"
	"fmt"
	"reflect"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	chdriver "github.com/corker/clickhouse-mcp/internal/clickhouse"
	"github.com/corker/clickhouse-mcp/internal/query"
)

const DefaultRowLimit = 100

type runQueryArgs struct {
	SQL   string `json:"sql" jsonschema:"a single row-returning SQL statement (SELECT/WITH/SHOW/DESCRIBE/EXPLAIN/EXISTS)"`
	Limit int    `json:"limit,omitempty" jsonschema:"max rows to return; defaults to 100"`
}

func RegisterRunQuery(server *mcp.Server, conn driver.Conn) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "run_query",
		Description: "Run a single row-returning ClickHouse query (SELECT/WITH/SHOW/" +
			"DESCRIBE/EXPLAIN/EXISTS) and return typed rows plus each column's type. " +
			"Large integers and decimals are returned as strings to avoid precision " +
			"loss; use column_types to tell those from real strings. Use run_statement " +
			"for writes and DDL.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args runQueryArgs) (*mcp.CallToolResult, query.Result, error) {
		return runQuery(ctx, conn, args)
	})
}

func runQuery(ctx context.Context, conn driver.Conn, args runQueryArgs) (*mcp.CallToolResult, query.Result, error) {
	if query.ContainsMultipleStatements(args.SQL) {
		return nil, query.Result{}, fmt.Errorf("send one statement per call; multiple statements separated by ';' are not supported")
	}
	if !query.IsRowReturning(args.SQL) {
		return nil, query.Result{}, fmt.Errorf("run_query is for row-returning statements (SELECT/WITH/SHOW/DESCRIBE/EXPLAIN/EXISTS); use run_statement for writes and DDL")
	}
	if query.HasUnsupportedOutputClause(args.SQL) {
		return nil, query.Result{}, fmt.Errorf("FORMAT and INTO OUTFILE are not supported; results are returned as structured rows")
	}

	limit := args.Limit
	if limit <= 0 {
		limit = DefaultRowLimit
	}

	result, err := execBounded(ctx, conn, args.SQL, limit)
	return nil, result, err
}

// execBounded runs a row-returning statement under the cap ceiling. SELECT/WITH
// are wrapped with LIMIT displayLimit+1 to detect truncation; other statements
// run as-is (the throw-mode cap is their backstop). Whether the connected user
// may run the statement is ClickHouse's call — a non-read statement here just
// returns whatever the driver/server says.
func execBounded(ctx context.Context, conn driver.Conn, sql string, displayLimit int) (query.Result, error) {
	bounded := query.Bound(sql, displayLimit+1)

	qctx := chdriver.DefaultReadContext(ctx)

	rows, err := conn.Query(qctx, bounded)
	if err != nil {
		return query.Result{}, fmt.Errorf("query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	columns, columnTypes, fetched, err := scanRows(rows)
	if err != nil {
		return query.Result{}, err
	}
	return query.Shape(columns, columnTypes, fetched, displayLimit, query.HasTopLevelOrderBy(sql)), nil
}

// scanRows materializes all rows. The driver rejects Scan into *interface{}, so a
// typed destination is allocated per column from ColumnTypes().ScanType().
func scanRows(rows driver.Rows) (columns, columnTypes []string, fetched [][]any, err error) {
	columns = rows.Columns()
	cts := rows.ColumnTypes()
	columnTypes = make([]string, len(cts))
	for i, ct := range cts {
		columnTypes[i] = ct.DatabaseTypeName()
	}
	fetched = [][]any{}
	for rows.Next() {
		dest := make([]any, len(cts))
		for i, ct := range cts {
			dest[i] = reflect.New(ct.ScanType()).Interface()
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, nil, fmt.Errorf("scan row: %w", err)
		}
		row := make([]any, len(cts))
		for i := range cts {
			row[i] = query.ToJSONValue(reflect.ValueOf(dest[i]).Elem().Interface())
		}
		fetched = append(fetched, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("read rows: %w", err)
	}
	return columns, columnTypes, fetched, nil
}
