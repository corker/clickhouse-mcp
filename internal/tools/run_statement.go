package tools

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/ClickHouse/clickhouse-go/v2/lib/proto"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type runStatementArgs struct {
	SQL string `json:"sql" jsonschema:"a single statement to execute (INSERT, ALTER, CREATE, DROP, etc.)"`
}

type runStatementOutput struct {
	OK          bool   `json:"ok" jsonschema:"true when the statement executed without error"`
	RowsWritten uint64 `json:"rows_written" jsonschema:"rows the server reported writing; 0 for DDL and statements that write nothing"`
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
	// The native Exec returns no affected-row count, but the server streams it via
	// the progress protocol; sum WroteRows across progress packets.
	var wrote atomic.Uint64
	pctx := clickhouse.Context(ctx, clickhouse.WithProgress(func(p *proto.Progress) {
		wrote.Add(p.WroteRows)
	}))
	if err := conn.Exec(pctx, args.SQL); err != nil {
		return nil, runStatementOutput{}, fmt.Errorf("statement failed: %w", err)
	}
	return nil, runStatementOutput{OK: true, RowsWritten: wrote.Load()}, nil
}
