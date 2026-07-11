package tools

import (
	"context"
	"strings"
	"testing"
)

// The tool gates reject before touching the connection, so they are pure logic
// and run without a container (conn is nil — a live call would panic, proving
// the gate short-circuits first).

func TestRunQuery_RejectsNonRowReturning(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"insert", "INSERT INTO t VALUES (1)"},
		{"create", "CREATE TABLE t (x UInt8) ENGINE=Memory"},
		{"drop", "DROP TABLE t"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runQuery(context.Background(), nil, runQueryArgs{SQL: tt.sql})
			if err == nil || !strings.Contains(err.Error(), "run_statement") {
				t.Errorf("want rejection pointing to run_statement, got: %v", err)
			}
		})
	}
}

// Blank input is rejected up front by both tools — before the row-returning
// routing (so it is not misrouted) and before conn (nil here).
func TestBothTools_RejectBlank(t *testing.T) {
	for _, sql := range []string{"", "   ", "-- only a comment"} {
		if _, _, err := runQuery(context.Background(), nil, runQueryArgs{SQL: sql}); err == nil || !strings.Contains(err.Error(), "provide a SQL statement") {
			t.Errorf("run_query(%q) want blank rejection, got: %v", sql, err)
		}
		if _, _, err := runStatement(context.Background(), nil, runStatementArgs{SQL: sql}); err == nil || !strings.Contains(err.Error(), "provide a SQL statement") {
			t.Errorf("run_statement(%q) want blank rejection, got: %v", sql, err)
		}
	}
}

// Both tools reject a multi-statement before touching conn: run_query would
// otherwise leak its LIMIT wrapper in a syntax error, and run_statement would
// silently execute only the first statement (verified; ClickHouse #66931).
func TestBothTools_RejectMultipleStatements(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"two-selects", "SELECT 1; SELECT 2"},
		{"select-then-write", "SELECT 1; INSERT INTO t VALUES (1)"}, // multi-statement gate must win over row-returning routing
		{"write-then-select", "INSERT INTO t VALUES (1); SELECT 2"}, // and the reverse, so run_statement rejects before ExecWritten
		{"two-writes", "INSERT INTO t VALUES (1); INSERT INTO t VALUES (2)"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, _, qErr := runQuery(context.Background(), nil, runQueryArgs{SQL: tt.sql})
			if qErr == nil || !strings.Contains(qErr.Error(), "one statement per call") {
				t.Errorf("run_query want one-statement rejection, got: %v", qErr)
			}
			_, _, sErr := runStatement(context.Background(), nil, runStatementArgs{SQL: tt.sql})
			if sErr == nil || !strings.Contains(sErr.Error(), "one statement per call") {
				t.Errorf("run_statement want one-statement rejection, got: %v", sErr)
			}
		})
	}
}

func TestRunQuery_RejectsOutputClauses(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"format", "SELECT 1 FORMAT JSON"},
		{"outfile", "SELECT 1 INTO OUTFILE '/tmp/x'"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runQuery(context.Background(), nil, runQueryArgs{SQL: tt.sql})
			if err == nil || !strings.Contains(err.Error(), "not supported") {
				t.Errorf("want a clear 'not supported' rejection, got: %v", err)
			}
		})
	}
}

func TestRunStatement_RejectsRowReturning(t *testing.T) {
	cases := []struct{ name, sql string }{
		{"select", "SELECT 1"},
		{"show", "SHOW DATABASES"},
		{"describe", "DESCRIBE system.numbers"},
		{"with", "WITH x AS (SELECT 1) SELECT * FROM x"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runStatement(context.Background(), nil, runStatementArgs{SQL: tt.sql})
			if err == nil || !strings.Contains(err.Error(), "run_query") {
				t.Errorf("want rejection pointing to run_query, got: %v", err)
			}
		})
	}
}
