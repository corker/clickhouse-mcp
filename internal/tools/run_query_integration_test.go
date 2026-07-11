//go:build integration

package tools

import (
	"context"
	"strings"
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
	if res.Count != 5 || len(res.Rows) != 5 {
		t.Errorf("expected 5 rows, got %d", res.Count)
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
	if res.Truncated || res.Count != 3 {
		t.Errorf("expected 3 rows not truncated, got %d truncated=%v", res.Count, res.Truncated)
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

func TestRunQuery_ColumnTypes(t *testing.T) {
	conn := testsupport.Start(t)
	// column_types tells the caller which stringified values are numerics.
	_, res, err := runQuery(context.Background(), conn, runQueryArgs{
		SQL: "SELECT toUInt64(1) AS u, CAST(1.5 AS Decimal(10,2)) AS d, 'hi' AS s",
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.ColumnTypes) != len(res.Columns) {
		t.Fatalf("column_types must align with columns: types=%v cols=%v", res.ColumnTypes, res.Columns)
	}
	if res.ColumnTypes[0] != "UInt64" || res.ColumnTypes[2] != "String" {
		t.Errorf("expected UInt64 and String types, got %v", res.ColumnTypes)
	}
	if !strings.HasPrefix(res.ColumnTypes[1], "Decimal") {
		t.Errorf("expected a Decimal type for column d, got %q", res.ColumnTypes[1])
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

func TestRunQuery_VariantBigInt(t *testing.T) {
	conn := testsupport.Start(t)
	// Variant(UInt64, String) holding a >2^53 value must serialize as a string,
	// not a lossy JSON number (the LSP re-audit finding).
	sql := "SELECT CAST(toUInt64(18446744073709551615) AS Variant(UInt64, String)) AS v SETTINGS allow_experimental_variant_type=1"
	_, res, err := runQuery(context.Background(), conn, runQueryArgs{SQL: sql})
	if err != nil {
		t.Skipf("Variant unsupported on this server: %v", err)
	}
	if got := res.Rows[0][0]; got != "18446744073709551615" {
		t.Errorf("Variant(UInt64) should serialize as a string, got %#v", got)
	}
}

func TestRunQuery_SmallStatements(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()
	for _, sql := range []string{"SHOW DATABASES", "DESCRIBE system.numbers", "EXPLAIN SELECT 1"} {
		_, res, err := runQuery(ctx, conn, runQueryArgs{SQL: sql})
		if err != nil {
			t.Errorf("%q should run: %v", sql, err)
			continue
		}
		// These statements always return at least one row (a db list, a column
		// description, a plan line) — assert the projection wasn't dropped.
		if res.Count == 0 || len(res.Columns) == 0 {
			t.Errorf("%q: expected rows and columns, got rowcount=%d cols=%v", sql, res.Count, res.Columns)
		}
	}
	// DESCRIBE system.numbers must describe the `number` column.
	_, res, err := runQuery(ctx, conn, runQueryArgs{SQL: "DESCRIBE system.numbers"})
	if err != nil || len(res.Rows) == 0 || res.Rows[0][0] != "number" {
		t.Errorf("DESCRIBE should list the number column, got rows=%v err=%v", res.Rows, err)
	}
}

// run_query gates on row-returning statements: a write is rejected up front and
// must NOT execute (the driver's Query path would otherwise perform the INSERT
// and then error on the empty result). Writes belong on run_statement.
func TestRunQuery_RejectsWriteWithoutExecuting(t *testing.T) {
	conn, db := testsupport.Database(t)
	ctx := context.Background()
	if err := conn.Exec(ctx, "CREATE TABLE "+db+".t (x UInt8) ENGINE=Memory"); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, _, err := runQuery(ctx, conn, runQueryArgs{SQL: "INSERT INTO " + db + ".t VALUES (1)"})
	if err == nil {
		t.Fatal("an INSERT via run_query should be rejected")
	}
	if !strings.Contains(err.Error(), "run_statement") {
		t.Errorf("rejection should point to run_statement, got: %v", err)
	}
	// The write must not have happened.
	var n uint64
	if err := conn.QueryRow(ctx, "SELECT count() FROM "+db+".t").Scan(&n); err != nil || n != 0 {
		t.Errorf("rejected write must not execute: count=%d err=%v", n, err)
	}
}

// FORMAT / INTO OUTFILE are rejected with a clear message (unwrapped they would
// yield no rows, and INTO OUTFILE could write a file server-side).
func TestRunQuery_RejectsOutputClauses(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()
	for _, sql := range []string{"SELECT 1 FORMAT JSON", "SELECT 1 INTO OUTFILE '/tmp/x'"} {
		if _, _, err := runQuery(ctx, conn, runQueryArgs{SQL: sql}); err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("%q: want a clear 'not supported' rejection, got: %v", sql, err)
		}
	}
	// A function prefixed with format must still work (not be rejected).
	if _, _, err := runQuery(ctx, conn, runQueryArgs{SQL: "SELECT formatDateTime(now(),'%Y') AS y"}); err != nil {
		t.Errorf("formatDateTime should work, got: %v", err)
	}
}

// A SELECT ending in a trailing line comment must still execute AND return the
// right rows: the wrap must put ") LIMIT n" on its own line so the comment does
// not swallow it (a swallowed LIMIT executes fine but returns the wrong rows —
// so this asserts values, not just the absence of an error).
func TestRunQuery_TrailingLineComment(t *testing.T) {
	conn := testsupport.Start(t)
	ctx := context.Background()

	// Trailing comment on a scalar select.
	_, res, err := runQuery(ctx, conn, runQueryArgs{SQL: "SELECT 1 AS n -- trailing comment", Limit: 5})
	if err != nil || res.Count != 1 || res.Rows[0][0] != uint8(1) {
		t.Errorf("scalar with trailing comment: rows=%v err=%v", res.Rows, err)
	}

	// The LIMIT before the comment must survive the wrap (3 rows, not more/fewer).
	_, res, err = runQuery(ctx, conn, runQueryArgs{SQL: "SELECT number FROM system.numbers LIMIT 3 -- note", Limit: 5})
	if err != nil || res.Count != 3 || res.Truncated {
		t.Errorf("trailing comment must not swallow LIMIT 3: rowcount=%d truncated=%v err=%v", res.Count, res.Truncated, err)
	}

	// `--` inside a string literal must round-trip, not be stripped as a comment.
	_, res, err = runQuery(ctx, conn, runQueryArgs{SQL: "SELECT '--x' AS s", Limit: 5})
	if err != nil || res.Rows[0][0] != "--x" {
		t.Errorf("-- inside a literal must round-trip: rows=%v err=%v", res.Rows, err)
	}
}
