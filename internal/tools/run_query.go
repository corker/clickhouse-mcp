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

	// Bound: SELECT/WITH wrap as SELECT * FROM (...) LIMIT n+1 so we can detect
	// truncation; small statements run as-is under the cap backstop.
	sql := query.Bound(args.SQL, class, limit+1)

	qctx := chdriver.ReadOnlyCappedContext(ctx,
		chdriver.DefaultMaxResultRows, chdriver.DefaultMaxResultBytes, chdriver.DefaultMaxExecSeconds)

	rows, err := conn.Query(qctx, sql)
	if err != nil {
		return nil, query.Result{}, fmt.Errorf("query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()

	columns := rows.Columns()
	cts := rows.ColumnTypes()
	fetched := [][]any{}
	for rows.Next() {
		// The driver rejects Scan into *interface{}; allocate a typed destination
		// per column from ColumnTypes().ScanType() and deref after scanning.
		dest := make([]any, len(cts))
		for i, ct := range cts {
			dest[i] = reflect.New(ct.ScanType()).Interface()
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, query.Result{}, fmt.Errorf("scan row: %w", err)
		}
		row := make([]any, len(cts))
		for i := range cts {
			row[i] = query.ToJSONValue(reflect.ValueOf(dest[i]).Elem().Interface())
		}
		fetched = append(fetched, row)
	}
	if err := rows.Err(); err != nil {
		return nil, query.Result{}, fmt.Errorf("read rows: %w", err)
	}

	result := query.Shape(columns, fetched, limit, query.HasTopLevelOrderBy(args.SQL))
	return nil, result, nil
}
