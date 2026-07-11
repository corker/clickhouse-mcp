package tools

import (
	"context"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	chdriver "github.com/corker/clickhouse-mcp/internal/clickhouse"
	"github.com/corker/clickhouse-mcp/internal/query"
)

type runStatementArgs struct {
	SQL string `json:"sql" jsonschema:"a single statement to execute (INSERT, ALTER, CREATE, DROP, etc.)"`
}

type runStatementOutput struct {
	RowsWritten uint64 `json:"rows_written" jsonschema:"rows the server reported writing (best-effort: 0 for DDL, and 0 on older ClickHouse servers that do not send write progress)"`
}

func RegisterRunStatement(server *mcp.Server, conn driver.Conn) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "run_statement",
		Description: "Execute a single ClickHouse statement that does not return rows " +
			"(INSERT, ALTER, CREATE, DROP, etc.) and report rows written. Whether the " +
			"connected user may run it is enforced by ClickHouse; a lack of privilege " +
			"comes back as its error. Use run_query for SELECT and other row-returning " +
			"statements.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args runStatementArgs) (*mcp.CallToolResult, runStatementOutput, error) {
		return runStatement(ctx, conn, args)
	})
}

func runStatement(ctx context.Context, conn driver.Conn, args runStatementArgs) (*mcp.CallToolResult, runStatementOutput, error) {
	if query.IsBlank(args.SQL) {
		return nil, runStatementOutput{}, fmt.Errorf("provide a SQL statement to run")
	}
	// Reject before Exec: clickhouse-go runs only the first statement of a
	// multi-statement write and silently drops the rest (verified; ClickHouse #66931).
	if query.ContainsMultipleStatements(args.SQL) {
		return nil, runStatementOutput{}, fmt.Errorf("send one statement per call; multiple statements separated by ';' are not supported")
	}
	if query.IsRowReturning(args.SQL) {
		return nil, runStatementOutput{}, fmt.Errorf("run_statement is for statements that do not return rows; use run_query for SELECT/WITH/SHOW/DESCRIBE/EXPLAIN/EXISTS")
	}
	wrote, err := chdriver.ExecWritten(ctx, conn, args.SQL)
	if err != nil {
		return nil, runStatementOutput{}, fmt.Errorf("statement failed: %w", err)
	}
	return nil, runStatementOutput{RowsWritten: wrote}, nil
}
