//go:build integration

package tools

import (
	"context"
	"testing"

	"github.com/corker/clickhouse-mcp/internal/clickhouse/testsupport"
)

func TestRunQuery_Truncation(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()

	// 100 rows available, display limit 5 -> truncated, 5 shown.
	_, res, err := runQuery(ctx, conn, runQueryArgs{SQL: "SELECT number FROM system.numbers LIMIT 100", Limit: 5})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !res.Truncated {
		t.Error("expected truncated=true")
	}
	if res.RowCount != 5 || len(res.Rows) != 5 {
		t.Errorf("expected 5 rows, got %d", res.RowCount)
	}
	if res.Note == "" {
		t.Error("expected a truncation note")
	}
}

func TestRunQuery_NotTruncated(t *testing.T) {
	conn := testsupport.Start(t)
	_, res, err := runQuery(context.Background(), conn, runQueryArgs{SQL: "SELECT number FROM system.numbers LIMIT 3", Limit: 5})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Truncated || res.RowCount != 3 {
		t.Errorf("expected 3 rows not truncated, got %d truncated=%v", res.RowCount, res.Truncated)
	}
}

func TestRunQuery_Types(t *testing.T) {
	conn := testsupport.Start(t)
	_, res, err := runQuery(context.Background(), conn, runQueryArgs{
		SQL: `SELECT toUInt64(18446744073709551615) AS u64, [1,2,3] AS arr, CAST(NULL AS Nullable(String)) AS nul`,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	row := res.Rows[0]
	if row[0] != "18446744073709551615" {
		t.Errorf("UInt64 should be a string, got %#v", row[0])
	}
	if arr, ok := row[1].([]any); !ok || len(arr) != 3 {
		t.Errorf("Array(UInt8) should be a JSON array of numbers, got %#v", row[1])
	}
	if row[2] != nil {
		t.Errorf("Nullable NULL should be nil, got %#v", row[2])
	}
}

func TestRunQuery_ArrayOfBigInts(t *testing.T) {
	conn := testsupport.Start(t)
	// Array(UInt64) elements must be strings, not lossy JSON numbers — the LSP fix.
	_, res, err := runQuery(context.Background(), conn, runQueryArgs{
		SQL: "SELECT [toUInt64(18446744073709551615), toUInt64(1)] AS arr",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	arr, ok := res.Rows[0][0].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("expected 2-element array, got %#v", res.Rows[0][0])
	}
	if arr[0] != "18446744073709551615" {
		t.Errorf("Array(UInt64) element should be a string, got %#v", arr[0])
	}
}

func TestRunQuery_SmallStatements(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()
	for _, sql := range []string{"SHOW DATABASES", "DESCRIBE system.numbers", "EXPLAIN SELECT 1"} {
		if _, _, err := runQuery(ctx, conn, runQueryArgs{SQL: sql}); err != nil {
			t.Errorf("%q should run: %v", sql, err)
		}
	}
}

func TestRunQuery_RejectsWrites(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()
	for _, sql := range []string{"INSERT INTO t VALUES (1)", "DROP TABLE t", "CREATE TABLE t (x UInt8) ENGINE=Memory"} {
		if _, _, err := runQuery(ctx, conn, runQueryArgs{SQL: sql}); err == nil {
			t.Errorf("%q should be rejected by the allowlist", sql)
		}
	}
}
