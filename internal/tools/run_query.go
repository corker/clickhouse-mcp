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

// DefaultRowLimit is the display limit applied when the caller omits Limit.
const DefaultRowLimit = 100

type runQueryArgs struct {
	SQL   string `json:"sql" jsonschema:"the read-only SQL to run (SELECT/WITH/SHOW/DESCRIBE/EXPLAIN/EXISTS)"`
	Limit int    `json:"limit,omitempty" jsonschema:"max rows to return; defaults to 100"`
}

// RegisterRunQuery registers the run_query tool: a guarded, read-only query
// executor returning typed structured output with explicit truncation.
func RegisterRunQuery(server *mcp.Server, conn driver.Conn) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "run_query",
		Description: "Run a read-only ClickHouse query and return typed rows. " +
			"Only SELECT/WITH/SHOW/DESCRIBE/EXPLAIN/EXISTS are allowed. Large " +
			"integers and decimals are returned as strings to avoid precision loss.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args runQueryArgs) (*mcp.CallToolResult, query.Result, error) {
		return runQuery(ctx, conn, args)
	})
}

func runQuery(ctx context.Context, conn driver.Conn, args runQueryArgs) (*mcp.CallToolResult, query.Result, error) {
	class := query.Classify(args.SQL)
	if class == query.ClassRejected {
		return nil, query.Result{}, fmt.Errorf("only read-only queries are allowed (SELECT, WITH, SHOW, DESCRIBE, EXPLAIN, EXISTS)")
	}

	limit := args.Limit
	if limit <= 0 {
		limit = DefaultRowLimit
	}

	result, err := execBounded(ctx, conn, args.SQL, class, limit)
	return nil, result, err
}

// execBounded runs an already-classified read query through the guarded path and
// shapes the result. It is the reusable core the inspection tools (list_tables,
// list_databases) call with their own canned SQL, so they need not re-classify or
// duplicate the scan loop. SELECT/WITH are wrapped with LIMIT displayLimit+1 to
// detect truncation; small statements run as-is under the cap backstop.
func execBounded(ctx context.Context, conn driver.Conn, sql string, class query.StmtClass, displayLimit int) (query.Result, error) {
	bounded := query.Bound(sql, class, displayLimit+1)

	qctx := chdriver.ReadOnlyCappedContext(ctx,
		chdriver.DefaultMaxResultRows, chdriver.DefaultMaxResultBytes, chdriver.DefaultMaxExecSeconds)

	rows, err := conn.Query(qctx, bounded)
	if err != nil {
		return query.Result{}, fmt.Errorf("query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	columns, fetched, err := scanRows(rows)
	if err != nil {
		return query.Result{}, err
	}
	return query.Shape(columns, fetched, displayLimit, query.HasTopLevelOrderBy(sql)), nil
}

// scanRows materializes all rows into JSON-safe positional values. The driver
// rejects Scan into *interface{}, so a typed destination is allocated per column
// from ColumnTypes().ScanType(), then each scanned value is routed through
// query.ToJSONValue.
func scanRows(rows driver.Rows) (columns []string, fetched [][]any, err error) {
	columns = rows.Columns()
	cts := rows.ColumnTypes()
	fetched = [][]any{}
	for rows.Next() {
		dest := make([]any, len(cts))
		for i, ct := range cts {
			dest[i] = reflect.New(ct.ScanType()).Interface()
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, fmt.Errorf("scan row: %w", err)
		}
		row := make([]any, len(cts))
		for i := range cts {
			row[i] = query.ToJSONValue(reflect.ValueOf(dest[i]).Elem().Interface())
		}
		fetched = append(fetched, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read rows: %w", err)
	}
	return columns, fetched, nil
}
