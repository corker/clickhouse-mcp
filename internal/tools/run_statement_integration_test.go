//go:build integration

package tools

import (
	"context"
	"testing"

	"github.com/corker/clickhouse-mcp/internal/clickhouse/testsupport"
)

func TestRunStatement_WriteAndDDL(t *testing.T) {
	conn, db := testsupport.Database(t)
	ctx := context.Background()

	// DDL: ok, no rows written.
	_, out, err := runStatement(ctx, conn, runStatementArgs{
		SQL: "CREATE TABLE " + db + ".t (x UInt64) ENGINE=Memory",
	})
	if err != nil || out.RowsWritten != 0 {
		t.Fatalf("create: rows=%d err=%v", out.RowsWritten, err)
	}

	// INSERT: rows_written reflects the server's count.
	_, out, err = runStatement(ctx, conn, runStatementArgs{
		SQL: "INSERT INTO " + db + ".t SELECT number FROM system.numbers LIMIT 42",
	})
	if err != nil || out.RowsWritten != 42 {
		t.Fatalf("insert: rows=%d err=%v", out.RowsWritten, err)
	}

	// The write is visible via run_query.
	_, res, err := runQuery(ctx, conn, runQueryArgs{SQL: "SELECT count() FROM " + db + ".t"})
	if err != nil || res.Rows[0][0] != "42" {
		t.Fatalf("count after insert: rows=%v err=%v", res.Rows, err)
	}
}

// A statement the connected user cannot run comes back as ClickHouse's error,
// not a server-side rejection — the server does not authorize.
func TestRunStatement_ServerRejectsBadSQL(t *testing.T) {
	conn, db := testsupport.Database(t)
	if _, _, err := runStatement(context.Background(), conn, runStatementArgs{
		SQL: "INSERT INTO " + db + ".does_not_exist VALUES (1)",
	}); err == nil {
		t.Error("insert into a missing table should return the server error")
	}
}
