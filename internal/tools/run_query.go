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
	SQL   string `json:"sql" jsonschema:"the read-only SQL to run (SELECT/WITH/SHOW/DESCRIBE/EXPLAIN/EXISTS)"`
	Limit int    `json:"limit,omitempty" jsonschema:"max rows to return; defaults to 100"`
}

func RegisterRunQuery(server *mcp.Server, conn driver.Conn) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "run_query",
		Description: "Run a single read-only ClickHouse query and return typed rows " +
			"plus each column's type. Only SELECT/WITH/SHOW/DESCRIBE/EXPLAIN/EXISTS " +
			"are allowed. Large integers and decimals are returned as strings to " +
			"avoid precision loss; use column_types to tell those from real strings.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args runQueryArgs) (*mcp.CallToolResult, query.Result, error) {
		return runQuery(ctx, conn, args)
	})
}

func runQuery(ctx context.Context, conn driver.Conn, args runQueryArgs) (*mcp.CallToolResult, query.Result, error) {
	class := query.Classify(args.SQL)
	if class == query.ClassRejected {
		return nil, query.Result{}, fmt.Errorf("only read-only queries are allowed (SELECT, WITH, SHOW, DESCRIBE, EXPLAIN, EXISTS)")
	}
	if query.HasUnsupportedOutputClause(args.SQL) {
		return nil, query.Result{}, fmt.Errorf("FORMAT and INTO OUTFILE are not supported; results are returned as structured rows")
	}
	if query.ContainsMultipleStatements(args.SQL) {
		return nil, query.Result{}, fmt.Errorf("only one statement per call is supported; send a single read-only query")
	}

	limit := args.Limit
	if limit <= 0 {
		limit = DefaultRowLimit
	}

	result, err := execBounded(ctx, conn, args.SQL, class, limit)
	return nil, result, err
}

// execBounded is the reusable guarded path: a caller passes trusted SQL and its
// already-decided class. SELECT/WITH are wrapped with LIMIT displayLimit+1 to
// detect truncation; small statements run as-is under the cap backstop.
func execBounded(ctx context.Context, conn driver.Conn, sql string, class query.StmtClass, displayLimit int) (query.Result, error) {
	bounded := query.Bound(sql, class, displayLimit+1)

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
